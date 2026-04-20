package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/urfave/cli/v3"

	"github.com/cloudkucooland/go-daikin"
)

const (
	DeepCoolTemp             = 16.5               // need to be C even if the thermostat is set to display in F
	DeepCoolHeatTemp         = DeepCoolTemp + 5.5 // Just needs to be more than DeepCoolTemp
	DeepCoolMaxImportWatts   = 500000             // if in deepcool mode, how much can we "overdraw" before switching to schedule?
	DeepCoolMinExportWatts   = -1100000           // if in schedule, how much do we need to be exporting before we start deepcool (negative for export)
	DeepCoolMinorExportWatts = -200000            // for demand-response-based "not-so-deep" cooling
)

func main() {
	cmd := &cli.Command{
		Name:  "deepcool",
		Usage: "Deep cool house when exporting; depends on Daikin and InfluxDB",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "monitor-only", Value: false, Usage: "monitor without engaging deep cooling"},
			// &cli.StringFlag{Name: "energy-bucket", Value: "energy", Usage: "influxdb bucket for energy data (ro)"},
			&cli.StringFlag{Name: "daikin-bucket", Value: "daikin", Usage: "influxdb bucket for daikin data (rw)"},
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
	slog.Info("Setting up InfluxDB", "host", os.Getenv("INFLUX_HOST"), "bucket", cmd.String("daikin-bucket"))
	ok, err := influx.Health(ctx)
	if err != nil || ok.Status != "pass" {
		slog.Error("influxdb health check failed", "error", err)
	}
	writeAPI := influx.WriteAPIBlocking(os.Getenv("INFLUX_ORG"), cmd.String("daikin-bucket"))
	queryAPI := influx.QueryAPI(os.Getenv("INFLUX_ORG"))
	defer influx.Close()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for _, device := range d.Devices {
				nctx, ncancel := context.WithTimeout(ctx, 30*time.Second)
				avgNetMW, err := getAveragePower(nctx, queryAPI)
				if err != nil {
					slog.Error("error getting average power", "err", err)
					if err := device.SetModeSchedule(ctx); err != nil {
						slog.Error("unable to switch to schedule", "error", err)
					} // failsafe, use primary context
					ncancel()
					continue
				}

				info, err := device.GetInfo(nctx)
				if err != nil {
					slog.Error("error polling daikin", "err", err)
					if err := device.SetModeSchedule(ctx); err != nil { // failsafe, use primary context
						slog.Error("unable to switch to schedule", "error", err)
					}
					ncancel()
					continue
				}

				if err := logStats(nctx, writeAPI, device.Name, info); err != nil {
					slog.Error("unable to log to influx", "error", err)
				}

				if info.Mode != daikin.ModeCool {
					slog.Info("deepcool disabled in auto/heat modes")
					ncancel()
					continue
				}

				/* SetDenumidifySetpoint returns a 403 -- research in the go-daikin library
				if info.IndoorHumidity > 60 && info.DehumSetpoint != 45 {
					slog.Info("High humidity detected, lowering dehum setpoint", "current", info.IndoorHumidity)
					if err := device.SetDehumidifySetpoint(nctx, 45); err != nil {
						slog.Error("unable to lower dehum setpoint", "error", err)
					}
				} else if info.IndoorHumidity < 50 && info.DehumSetpoint != 55 {
					slog.Info("Humidity stabilized, relaxing dehum setpoint")
					if err := device.SetDehumidifySetpoint(nctx, 55); err != nil { // Return to a more efficient target
						slog.Error("unable to restore dehum setpoint", "error", err)
					}
				} */

				mo := cmd.Bool("monitor-only")

				switch {
				case avgNetMW < DeepCoolMinExportWatts:
					// STATE: MAXIMUM EXPORT -> FULL DEEP COOL
					slog.Info("state: maximum export")
					if info.ScheduleEnabled {
						slog.Info("Heavy export: Engaging Full Deep Cool", "watts", avgNetMW/1000.0)
						if !mo {
							if err := device.SetTemps(nctx, daikin.ModeCool, DeepCoolHeatTemp, DeepCoolTemp); err != nil {
								slog.Error("unable to set deep cool temps", "error", err)
							}
						}
					}
					// Disable DR so it doesn't try to offset our already low manual setpoint
					if info.DRIsActive && !mo {
						if err := device.SetDemandResponse(nctx, false, 0); err != nil {
							slog.Error("unable to clear demand-response settings", "error", err)
						}
					}

				case avgNetMW < DeepCoolMinorExportWatts:
					// STATE: MODERATE EXPORT -> SOFT COOL (DR)
					slog.Info("state: moderate")
					// If we were in Deep Cool, bring it back to schedule first
					if !info.ScheduleEnabled {
						slog.Info("Export reduced: Reverting to Schedule with DR Offset", "watts", avgNetMW/1000.0)
						if !mo {
							if err := device.SetModeSchedule(nctx); err != nil {
								slog.Error("unable to revert to schedule", "error", err)
							}
						}
					}
					// Nudge the schedule down by 2 degrees
					if !info.DRIsActive || info.DROffsetDegree != -2.0 {
						slog.Info("Minor export: Applying -2.0C Demand Response offset")
						if !mo {
							if err := device.SetDemandResponse(nctx, true, -2.0); err != nil {
								slog.Error("unable to enable demand-response cooling", "error", err)
							}
						}
					}

				case avgNetMW > DeepCoolMaxImportWatts:
					// STATE: IMPORTING -> FAILSAFE / NORMAL
					slog.Info("state: importing")
					if !info.ScheduleEnabled || info.DRIsActive {
						slog.Info("Importing: Disabling all overrides", "watts", avgNetMW/1000.0)
						// reverting to failsafe does not check for monitor-only
						if err := device.SetModeSchedule(nctx); err != nil {
							slog.Error("unable to revert to schedule", "error", err)
						}
						if err := device.SetDemandResponse(nctx, false, 0); err != nil {
							slog.Error("unable to clear demand response", "error", err)
						}
					}

				default:
					// STATE: NEUTRAL (Between -200W and +500W)
					// Do nothing. Stay in whatever mode we are in to prevent rapid toggling.
					slog.Info("Neutral power zone: maintaining current state")
				}
				ncancel()
			}
		}
	}
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
			"dr_active":        info.DRIsActive,
			"dr_offset":        info.DROffsetDegree,
			"dehum_setpoint":   info.DehumSetpoint,
		},
		time.Now())
	return w.WritePoint(ctx, p)
}

func getAveragePower(ctx context.Context, queryAPI api.QueryAPI) (float64, error) {
	query := fmt.Sprintf(`
		from(bucket: "%s")
		|> range(start: -15m)
		|> filter(fn: (r) => r["_measurement"] == "emeter")
		|> filter(fn: (r) => r["alias"] =~ /net-consumption-L[12]/)
		|> filter(fn: (r) => r["_field"] == "PowerMW")
		|> mean()
		|> group()
		|> sum()`, os.Getenv("INFLUX_BUCKET"))

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
