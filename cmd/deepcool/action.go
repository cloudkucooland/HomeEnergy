package main

import (
	"github.com/cloudkucooland/go-daikin"
	"math"
)

type CoolingAction int

const (
	ActionNone CoolingAction = iota
	ActionUseTheSolar
	ActionRevertToSchedule
)

func evaluateCoolingAction(avgNetMW float64, info *daikin.Info, forecast *Forecast, baselineCool float64) CoolingAction {
	// Detect manual override:
	// 1. API explicitly says schedule is disabled.
	// 2. OR current setpoint deviates from our captured schedule baseline.
	isManual := !info.ScheduleEnabled
	if baselineCool > 0 && math.Abs(info.CoolSetpoint-baselineCool) > 0.5 {
		isManual = true
	}

	switch {
	case info.Mode != daikin.ModeCool:
		// not cooling, ensure we are on the schedule
		if isManual {
			return ActionRevertToSchedule
		}
	case info.OutdoorTemp < DeepCoolMinOutdoorTemp:
		// too cold outside, don't spend solar
		if isManual {
			return ActionRevertToSchedule
		}
	case forecast != nil && forecast.Low < DeepCoolOverrideNightLowTemp:
		// forecast says tonight will be cool, don't spend solar
		if isManual {
			return ActionRevertToSchedule
		}
	case avgNetMW > DeepCoolMaxImportMilliWatts:
		// we are importing, quit wasting money
		if isManual {
			return ActionRevertToSchedule
		}
	case avgNetMW < DeepCoolExportingMilliWatts || isManual:
		// we are exporting, OR we are already manual and need to calculate the equilibrium
		return ActionUseTheSolar
	}

	// we don't need to change states
	return ActionNone
}
