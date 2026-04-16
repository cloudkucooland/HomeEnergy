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
	DeepCoolTemp     = 62 // 16.5
	DeepCoolHeatTemp = 75
)

func main() {
	cmd := &cli.Command{
		Name:  "deepcool",
		Usage: "Deep cool house when exporting; depends on Daikin and InfluxDB",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "monitor-only", Value: false, Usage: "monitor without engaging deep cooling"},
		},
		Action: run,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cmd.Run(ctx, os.Args); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
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
	// writeAPI := influx.WriteAPI(os.Getenv("INFLUX_ORG"), os.Getenv("INFLUX_BUCKET"))
	queryAPI := influx.QueryAPI(os.Getenv("INFLUX_ORG"))
	defer influx.Close()

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			avgNetMW, err := getAveragePower(ctx, queryAPI)

			/* if dErr == nil {
				logStats(writeAPI, devName, info)
			} */

			if err != nil || avgNetMW >= 0 {
				fmt.Printf("[%s] Switching to Auto (Schedule)\n", time.Now().Format(time.Kitchen))
				daikin.SetSchedule(0)
			} else {
				fmt.Printf("[%s] Export detected (%.1fW). Engaging Deep Cool.\n",
					time.Now().Format(time.Kitchen), avgNetMW/1000.0)
				if !cmd.Bool("monitor-only") {
					daikin.SetDeepCool(0, DeepCoolTemp, DeepCoolHeatTemp)
				}
			}
		}
	}
}

/* func logStats(w api.WriteAPI, name string, info *daikin.DeviceInfo) {
	p := influxdb2.NewPoint("daikin_stats",
		map[string]string{"device": name},
		map[string]interface{}{
			"temp_indoor": info.TempIndoor,
			"hum_indoor":  info.HumIndoor,
			"csp":         info.CspActive,
		},
		time.Now())
	w.WritePoint(p)
} */

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
