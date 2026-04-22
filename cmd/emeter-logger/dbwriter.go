package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/cloudkucooland/go-kasa"
	"github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/urfave/cli/v3"
)

type emeterdata struct {
	DeviceID string
	Alias    string
	R        *kasa.EmeterRealtime
}

var (
	client   influxdb2.Client
	writeAPI api.WriteAPI
)

func setupdb(ctx context.Context, cmd *cli.Command) error {
	host := os.Getenv("INFLUX_HOST")
	token := os.Getenv("INFLUX_TOKEN")
	org := os.Getenv("INFLUX_ORG")
	bucket := os.Getenv("INFLUX_BUCKET")

	emlog.InfoContext(ctx, "Setting up Kasa InfluxDB", "host", host, "bucket", bucket)

	client = influxdb2.NewClient(host, token)

	ok, err := client.Health(ctx)
	if err != nil || ok.Status != "pass" {
		return fmt.Errorf("influxdb health check failed: %w", err)
	}

	// Use the async WriteAPI to handle batching automatically
	writeAPI = client.WriteAPI(org, bucket)

	// Log async errors
	go func() {
		for err := range writeAPI.Errors() {
			emlog.Error("kasa influx write error", "err", err)
		}
	}()

	return nil
}

func startDBWriter(ctx context.Context, r <-chan emeterdata) {
	for {
		select {
		case <-ctx.Done():
			writeAPI.Flush()
			client.Close()
			return
		case v, ok := <-r:
			if !ok {
				return
			}

			// Explicit int64 casts ensure the 'i' suffix is added in Line Protocol
			p := influxdb2.NewPoint("emeter",
				map[string]string{
					"device": v.DeviceID,
					"alias":  v.Alias,
				},
				map[string]interface{}{
					"slot":      int64(v.R.Slot),
					"VoltageMV": int64(v.R.VoltageMV),
					"CurrentMA": int64(v.R.CurrentMA),
					"PowerMW":   int64(v.R.PowerMW),
				},
				time.Now())

			writeAPI.WritePoint(p)
		}
	}
}
