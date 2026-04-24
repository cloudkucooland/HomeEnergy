package main

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/signal"
	"syscall"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/urfave/cli/v3"

	"github.com/cloudkucooland/go-daikin"
)

// remember, we are stuck with PowerMW as miliwatts, not megawatts, all values are in milliwatts.
// this is due to the field name in the go-envoy library

const (
	DeepCoolColdestTemp          = 19.0    // need to be C even if the thermostat is set to display in F
	DeepCoolMaxImportMilliWatts  = 500000  // if in deepcool mode, how much can we "overdraw" before switching to schedule?
	DeepCoolExportingMilliWatts  = -500000 // if in schedule, how much do we need to be exporting before we start deepcool (negative for export)
	DeepCoolMinOutdoorTemp       = 22.0    // Don't deepcool if it's not hot
	DeepCoolOverrideNightLowTemp = 18.0    // Don't deepcool if tonight will be cool anyway
	DeepCoolCloudyThreshold      = 84      // 0-100, 84 is "broken clouds"
	DeepCoolMaxDelta             = 3.0     // Max degrees to cool below indoor temp to prevent inverter ramp-up
)

func main() {
	cmd := &cli.Command{
		Name:  "deepcool",
		Usage: "Deep cool house when exporting; depends on Daikin and InfluxDB",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "monitor-only", Value: false, Usage: "monitor without engaging deep cooling"},
			&cli.StringFlag{Name: "energy-bucket", Value: "energy", Sources: cli.EnvVars("INFLUX_BUCKET"), Usage: "influxdb bucket for energy data (ro)"},
			&cli.StringFlag{Name: "daikin-bucket", Value: "daikin", Sources: cli.EnvVars("DAIKIN_BUCKET", "INFLUX_BUCKET"), Usage: "influxdb bucket for daikin data (rw)"},
			&cli.StringFlag{Name: "weather-bucket", Value: "weather", Sources: cli.EnvVars("WEATHER_BUCKET"), Usage: "influxdb bucket for weather data (rw)"},
		},
		Action: run,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Run(ctx, os.Args); err != nil {
		slog.Error("shutting down", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, cmd *cli.Command) error {
	d, err := daikin.New(ctx, os.Getenv("DAIKIN_EMAIL"), os.Getenv("DAIKIN_DEVELOPER_KEY"), os.Getenv("DAIKIN_API_KEY"))
	if err != nil {
		slog.Error("unable to connect to Daikin", "err", err)
		return err
	}
	if len(d.Devices) == 0 {
		return fmt.Errorf("no daikin devices found")
	}
	slog.Info("Starting deepcool", "daikin", d.Devices[0].Name)

	influx := influxdb2.NewClient(os.Getenv("INFLUX_HOST"), os.Getenv("INFLUX_TOKEN"))
	slog.Info("Setting up InfluxDB", "host", os.Getenv("INFLUX_HOST"), "daikin-bucket", cmd.String("daikin-bucket"), "energy-bucket", cmd.String("energy-bucket"))
	ok, err := influx.Health(ctx)
	if err != nil || ok.Status != "pass" {
		slog.Error("influxdb health check failed", "error", err)
	}
	writeAPI := influx.WriteAPIBlocking(os.Getenv("INFLUX_ORG"), cmd.String("daikin-bucket"))
	weatherWriteAPI := influx.WriteAPIBlocking(os.Getenv("INFLUX_ORG"), cmd.String("weather-bucket"))
	queryAPI := influx.QueryAPI(os.Getenv("INFLUX_ORG"))
	defer influx.Close()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	i := 0
	forecast, _ := fetchForecast(ctx)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			// Fetch weather context once per hour
			if i%60 == 0 {
				forecast, err = fetchForecast(ctx)
				if err != nil {
					slog.Error("unable to fetch weather forecast", "error", err)
				} else {
					if err := logForecast(ctx, weatherWriteAPI, forecast); err != nil {
						slog.Error("unable to log forecast", "error", err)
					}
				}
			}
			i++

			for _, device := range d.Devices {
				nctx, ncancel := context.WithTimeout(ctx, 30*time.Second)
				avgNetMW, err := getAveragePower(nctx, cmd, queryAPI)
				if err != nil {
					slog.Error("error getting average power", "err", err)
					ncancel()
					continue
				}

				info, err := device.GetInfo(nctx)
				if err != nil {
					slog.Error("error polling daikin", "err", err)
					ncancel()
					continue
				}

				if err := logStats(nctx, writeAPI, device.Name, info); err != nil {
					slog.Error("unable to log to influx", "error", err)
				}

				slog.Info("tick", "device", device.Name, "net_watts", fmt.Sprintf("%.2f", avgNetMW/1000.0), "mode", info.Mode, "indoor", info.IndoorTemp, "outdoor", info.OutdoorTemp, "schedule", info.ScheduleEnabled, "cool", info.CoolSetpoint, "heat", info.HeatSetpoint)

				action := evaluateCoolingAction(avgNetMW, info, forecast)

				switch action {
				case ActionRevertToSchedule:
					slog.Info("Snapping back to schedule settings", "reason", "evaluateCoolingAction", "indoor", info.IndoorTemp, "net_watts", avgNetMW/1000.0)
					if err := device.SetModeSchedule(nctx, true); err != nil {
						slog.Error("unable to clear schedule override", "error", err)
					}
				case ActionUseTheSolar:
					heat, cool := calculateDynamicSetpoint(info, avgNetMW)

					slog.Info("Dynamic cooling adjustment",
						"action", action,
						"net_watts", fmt.Sprintf("%.2f", avgNetMW/1000.0),
						"old_cool", info.CoolSetpoint,
						"new_cool", cool,
						"indoor", info.IndoorTemp,
						"outdoor", info.OutdoorTemp)

					mo := cmd.Bool("monitor-only")
					if !mo {
						if info.ScheduleEnabled {
							if err := device.SetModeSchedule(nctx, false); err != nil {
								slog.Error("unable to turn the schedule off", "error", err)
							}
						}

						// Deadband: Only update if the change is at least 1.0C
						if math.Abs(cool-info.CoolSetpoint) >= 1.0 {
							if err := device.SetTemps(nctx, daikin.ModeCool, heat, cool); err != nil {
								slog.Error("unable to apply dynamic setpoint", "error", err)
							}
						} else {
							slog.Debug("Nudge below deadband, skipping API call", "delta", math.Abs(cool-info.CoolSetpoint))
						}
					}
				case ActionNone:
					slog.Info("Neutral power zone or conditions unmet: maintaining current state")
				}
				ncancel()
			}
		}
	}
}

func calculateDynamicSetpoint(info *daikin.Info, avgNetMW float64) (float64, float64) {
	exportWatts := -avgNetMW / 1000.0 // mW to W (negative avgNetMW means export)

	// Efficiency Factor: Watts required to lower indoor temp by 1C.
	efficiencyFactor := 300.0 + max(0, info.OutdoorTemp-20.0)*35.0 // this is not fully verified yet

	// additionalDelta is how much further we can lower the setpoint.
	additionalDelta := exportWatts / efficiencyFactor

	// Target is current setpoint minus the additional affordable delta.
	targetCool := info.CoolSetpoint - additionalDelta

	// SAFETY CONSTRAINTS
	if targetCool < DeepCoolColdestTemp {
		targetCool = DeepCoolColdestTemp
	}

	// Inverter Ramp Protection
	if targetCool < info.IndoorTemp-DeepCoolMaxDelta {
		targetCool = info.IndoorTemp - DeepCoolMaxDelta
	}

	// Round to 0.1C
	targetCool = math.Round(targetCool*10) / 10
	targetHeat := targetCool - info.SetPointDelta

	return targetHeat, targetCool
}

func logStats(ctx context.Context, w api.WriteAPIBlocking, name string, info *daikin.Info) error {
	p := influxdb2.NewPoint("daikin_stats",
		map[string]string{"device": name},
		map[string]interface{}{
			"temp_indoor":      info.IndoorTemp,
			"hum_indoor":       info.IndoorHumidity,
			"temp_outdoor":     info.OutdoorTemp,
			"hum_outdoor":      info.OutdoorHumidity,
			"mode":             info.Mode,
			"schedule_enabled": info.ScheduleEnabled,
		},
		time.Now())
	return w.WritePoint(ctx, p)
}

func getAveragePower(ctx context.Context, cmd *cli.Command, queryAPI api.QueryAPI) (float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -15m)
		|> filter(fn: (r) => r["_measurement"] == "emeter")
		|> filter(fn: (r) => r["alias"] =~ /net-consumption-L[12]/)
		|> filter(fn: (r) => r["_field"] == "PowerMW")
		|> mean()
		|> group()
		|> sum()`, cmd.String("energy-bucket"))

	result, err := queryAPI.Query(ctx, query)
	if err != nil {
		return 0, err
	}

	if result.Next() {
		if val, ok := result.Record().Value().(float64); ok {
			return val, nil
		}
	}
	return 0, result.Err()
}
