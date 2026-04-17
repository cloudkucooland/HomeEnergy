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
)

const (
	DeepCoolTemp     = 16.5 // need to be C even if the thermostat is set to display in F
	DeepCoolHeatTemp = 20
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
	daikin, err := NewDaikin(os.Getenv("DAIKIN_EMAIL"), os.Getenv("DAIKIN_DEVELOPER_KEY"), os.Getenv("DAIKIN_API_KEY"))
	if err != nil {
		slog.Error("unable to connect to Daikin", "err", err)
		return err
	}

	influx := influxdb2.NewClient(os.Getenv("INFLUX_HOST"), os.Getenv("INFLUX_TOKEN"))
	writeAPI := influx.WriteAPI(os.Getenv("INFLUX_ORG"), cmd.String("daikin-bucket"))
	queryAPI := influx.QueryAPI(os.Getenv("INFLUX_ORG"))
	defer influx.Close()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			for i := range daikin.Devices {
				avgNetMW, err := getAveragePower(ctx, queryAPI)
				if err != nil {
					slog.Error("error getting average power", "err", err)
					daikin.SetSchedule(i) // failsafe
					continue
				}

				info, err := daikin.GetInfo(i)
				if err != nil {
					slog.Error("error pooling daikin", "err", err)
					daikin.SetSchedule(i) // failsafe
					continue
				}
				logStats(writeAPI, daikin.Devices[i].Name, info)

				if info.Mode != modeCool {
					slog.Error("deepcool disabled in auto/heat modes", "err", err)
					continue
				}

				isExportingSignificant := false
				if !info.ScheduleEnabled {
					// WE ARE ALREADY IN DEEP COOL.
					// Stay in it as long as we aren't importing more than 500W.
					// Logic: Keep true if net power is less than 500W (e.g., -2000 export up to +500 import)
					isExportingSignificant = avgNetMW < 500000
				} else {
					// WE ARE ON NORMAL SCHEDULE.
					// Only engage if exporting more than 1.1kW.
					// Logic: True only if net power is deep in the negatives (exporting).
					isExportingSignificant = avgNetMW < -1100000
				}

				if isExportingSignificant && info.ScheduleEnabled {
					slog.Info("Export detected, engaging deep cool", "watts", avgNetMW/1000.0)
					if !cmd.Bool("monitor-only") {
						daikin.SetDeepCool(i, DeepCoolHeatTemp, DeepCoolTemp)
					}
				} else if !isExportingSignificant && !info.ScheduleEnabled {
					slog.Info("import detected, reverting to schedule", "watts", avgNetMW/1000.0)
					daikin.SetSchedule(i)
				}
			}
		}
	}
}

func logStats(w api.WriteAPI, name string, info *Info) {
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
	w.WritePoint(p)
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
