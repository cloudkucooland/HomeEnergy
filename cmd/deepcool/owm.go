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

	if err := w.DailyByCoordinates(
		&owm.Coordinates{
			Longitude: -112.07,
			Latitude:  33.45,
		},
		5, // five days forecast
	); err != nil {
		return nil, err
	}
	fmt.Println(w)
	var f = Forecast{
		High:    23,
		Low:     16,
		Cloudy:  false,
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
