package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redgoose/daikin-skyport"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
	"github.com/urfave/cli/v3"
)

const (
	DeepCoolTemp = 16.5 // ~62°F
)

func main() {
	cmd := &cli.Command{
		Name:  "deepcool",
		Usage: "Deep cool house when exporting; depens on Daikin and InfluxDB",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "daikin-email", EnvVars: []string{"DAIKIN_EMAIL"}},
			&cli.StringFlag{Name: "daikin-pass", EnvVars: []string{"DAIKIN_PASS"}},
			&cli.StringFlag{Name: "influx-url", Value: "http://localhost:8086", EnvVars: []string{"INFLUX_HOST"}},
			&cli.StringFlag{Name: "influx-token", EnvVars: []string{"INFLUX_TOKEN"}},
			&cli.StringFlag{Name: "fan-mode", Value: "high", Usage: "fan mode during deep cool (quiet, low, med, high)"},
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
	d := daikin.New(cmd.String("daikin-email"), cmd.String("daikin-pass"))
	influx := influxdb2.NewClient(cmd.String("influx-url"), cmd.String("influx-token"))
	writeAPI := influx.WriteAPI(os.Getenv("INFLUX_ORG"), os.Getenv("INFLUX_BUCKET"))
	defer influx.Close()

	devices, err := d.GetDevices()
	if err != nil || len(*devices) == 0 {
		return fmt.Errorf("no daikin devices found")
	}
	devID := (*devices)[0].Id
	fanMode := parseFanMode(cmd.String("fan-mode"))

	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			avgNetMW, err := getAveragePower(ctx, influx)
			info, dErr := d.GetDeviceInfo(devID)
			
			if dErr == nil {
				logStats(writeAPI, info)
			}

			if err != nil || avgNetMW >= 0 {
                // failesafe if data is garbage
				if info.Mode != daikin.ModeAuto {
					fmt.Printf("[%s] Switching to Auto (Schedule)\n", time.Now().Format(time.Kitchen))
					d.SetMode(devID, daikin.ModeAuto)
					d.SetFanMode(devID, daikin.FanCirculateSched)
				}
			} else {
				fmt.Printf("[%s] Export detected (%.1fW). Engaging Deep Cool.\n", 
					time.Now().Format(time.Kitchen), avgNetMW/1000.0)
				
				d.SetMode(devID, daikin.ModeCool)
				d.SetTemp(devID, daikin.SetTempParams{
					CoolSetpoint: DeepCoolTemp,
					HeatSetpoint: DeepCoolTemp - 4.0,
				})
				d.SetFanSpeed(devID, fanMode)
			}
		}
	}
}

func logStats(w api.WriteAPI, info *daikin.DeviceInfo) {
	p := influxdb2.NewPoint("daikin_stats",
		map[string]string{"device": info.Name},
		map[string]interface{}{
			"temp_indoor": info.TempIndoor,
			"hum_indoor":  info.HumIndoor,
			"csp":         info.CspActive,
		},
		time.Now())
	w.WritePoint(p)
}

func parseFanMode(m string) daikin.FanCirculateSpeed {
	switch m {
	case "low": return daikin.FanCirculateSpeedLow
	case "med": return daikin.FanCirculateSpeedMed
	case "high": return daikin.FanCirculateSpeedHigh
	default: return daikin.FanCirculateSpeedLow
	}
}

func getAveragePower(ctx context.Context, client influxdb2.Client) (float64, error) {
	queryAPI := client.QueryAPI(os.Getenv("INFLUX_ORG"))
	
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
		if val, ok := result.Value().(float64); ok {
			return val, nil
		}
	}
	return 0, result.Err()
}
