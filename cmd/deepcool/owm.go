package main

import (
	"context"
	"fmt"
	"os"
	"time"

	owm "github.com/briandowns/openweathermap"
	influxdb2 "github.com/influxdata/influxdb-client-go/v2"
	"github.com/influxdata/influxdb-client-go/v2/api"
)

type Forecast struct {
	High    float64
	Low     float64
	Cloudy  bool
	ValidAt time.Time
}

func fetchForecast(ctx context.Context) (*Forecast, error) {
	key := os.Getenv("OWM_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("OWM_API_KEY not set")
	}

	if err := owm.ValidAPIKey(key); err != nil {
		return nil, fmt.Errorf("OWM_API_KEY invalid")
	}

	w, err := owm.NewForecast("5", "C", "EN", key)
	if err != nil {
		return nil, err
	}

	if err := w.DailyByZipcode(os.Getenv("OWM_ZIPCODE"), "US", 5); err != nil {
		return nil, err
	}
	ff := w.ForecastWeatherJson.(*owm.Forecast5WeatherData)

	now := time.Now()
	// Define "tomorrow" as midnight to midnight tomorrow local time
	tomorrowStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	tomorrowEnd := tomorrowStart.Add(24 * time.Hour)

	var high float64 = -100.0
	var low float64 = 100.0
	var cloudy bool
	var found bool

	for _, item := range ff.List {
		t := time.Unix(int64(item.Dt), 0)
		if t.Before(tomorrowStart) || !t.Before(tomorrowEnd) {
			continue
		}

		found = true
		if item.Main.TempMax > high {
			high = item.Main.TempMax
		}
		if item.Main.TempMin < low {
			low = item.Main.TempMin
		}
		if item.Clouds.All > DeepCoolCloudyThreshold {
			cloudy = true
		}
	}

	if !found {
		return nil, fmt.Errorf("no forecast data found for tomorrow")
	}

	var f = Forecast{
		High:    high,
		Low:     low,
		Cloudy:  cloudy,
		ValidAt: time.Now(),
	}
	return &f, nil
}

func logForecast(ctx context.Context, w api.WriteAPIBlocking, f *Forecast) error {
	if f == nil {
		return nil
	}
	p := influxdb2.NewPoint("weather_forecast",
		map[string]string{"source": "owm"},
		map[string]interface{}{
			"high":   f.High,
			"low":    f.Low,
			"cloudy": f.Cloudy,
		},
		f.ValidAt)
	return w.WritePoint(ctx, p)
}
