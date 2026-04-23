package main

import (
	"github.com/cloudkucooland/go-daikin"
	"testing"
)

func TestEvaluateCoolingAction(t *testing.T) {
	tests := []struct {
		name     string
		avgNetMW float64
		info     daikin.Info
		forecast *Forecast
		expected CoolingAction
	}{
		{
			name:     "Not Cool Mode",
			avgNetMW: -2000000,
			info:     daikin.Info{Mode: daikin.ModeHeat, OutdoorTemp: 30, ScheduleEnabled: true},
			forecast: nil,
			expected: ActionNone,
		},
		{
			name:     "Full Deep Cool",
			avgNetMW: -2000000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 30, ScheduleEnabled: true},
			forecast: nil,
			expected: ActionFullDeepCool,
		},
		{
			name:     "Moderate Nudge",
			avgNetMW: -300000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 30, ScheduleEnabled: true},
			forecast: nil,
			expected: ActionModerateNudge,
		},
		{
			name:     "Importing Revert",
			avgNetMW: 600000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 30, ScheduleEnabled: false},
			forecast: nil,
			expected: ActionRevertToSchedule,
		},
		{
			name:     "Neutral None",
			avgNetMW: 0,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 30, ScheduleEnabled: true},
			forecast: nil,
			expected: ActionNone,
		},
		{
			name:     "Outdoor Temp Too Low - Needs Revert",
			avgNetMW: -2000000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 20, ScheduleEnabled: false},
			forecast: nil,
			expected: ActionRevertToSchedule,
		},
		{
			name:     "Outdoor Temp Too Low - Already Schedule",
			avgNetMW: -2000000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 20, ScheduleEnabled: true},
			forecast: nil,
			expected: ActionNone,
		},
		{
			name:     "Night Temp Too Low - Needs Revert",
			avgNetMW: -2000000,
			info:     daikin.Info{Mode: daikin.ModeCool, OutdoorTemp: 30, ScheduleEnabled: false},
			forecast: &Forecast{Low: 15},
			expected: ActionRevertToSchedule,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			action := evaluateCoolingAction(tt.avgNetMW, &tt.info, tt.forecast)
			if action != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, action)
			}
		})
	}
}
