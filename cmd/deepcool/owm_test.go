package main

import (
	"fmt"
	"testing"
	"time"

	owm "github.com/briandowns/openweathermap"
)

func TestForecast(t *testing.T) {
	// Create mock 5-day data
	now := time.Now()
	ff := &owm.Forecast5WeatherData{
		List: []owm.Forecast5WeatherList{},
	}
	
	// generate 40 items (3 hours each)
	for i := 0; i < 40; i++ {
		dt := now.Add(time.Duration(i*3) * time.Hour)
		item := owm.Forecast5WeatherList{
			Dt: int(dt.Unix()),
			Main: owm.Main{
				TempMax: float64(20 + i),
				TempMin: float64(10 + i),
			},
			Clouds: owm.Clouds{
				All: 10 + i,
			},
		}
		ff.List = append(ff.List, item)
	}

	tomorrowStart := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
	tomorrowEnd := tomorrowStart.Add(24 * time.Hour)

	var high float64 = -100.0
	var low float64 = 100.0
	var cloudy bool
	var found bool

	for _, item := range ff.List {
		dt := time.Unix(int64(item.Dt), 0)
		if dt.Before(tomorrowStart) || !dt.Before(tomorrowEnd) {
			continue
		}
		found = true
		if item.Main.TempMax > high {
			high = item.Main.TempMax
		}
		if item.Main.TempMin < low {
			low = item.Main.TempMin
		}
		if item.Clouds.All > 20 { // DeepCoolCloudyThreshold
			cloudy = true
		}
	}

	if !found {
		t.Fatal("not found")
	}
	fmt.Printf("High: %f, Low: %f, Cloudy: %v\n", high, low, cloudy)
}
