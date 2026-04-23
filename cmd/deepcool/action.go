package main

import (
	"github.com/cloudkucooland/go-daikin"
)

type CoolingAction int

const (
	ActionNone CoolingAction = iota
	ActionUseTheSolar
	ActionRevertToSchedule
)

func evaluateCoolingAction(avgNetMW float64, info *daikin.Info, forecast *Forecast) CoolingAction {
	isManual := !info.ScheduleEnabled

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
		// forcast says tonight will be cool, no need to spend the solar, get a few pennies from the power company
		if isManual {
			return ActionRevertToSchedule
		}
	case avgNetMW > DeepCoolMaxImportMilliWatts:
		// we are importing, quit wasting money
		if isManual {
			return ActionRevertToSchedule
		}
	case avgNetMW < DeepCoolExportingMilliWatts:
		// we are exporting, use the solar
		// do not check isManual since the target temp needs to be calculated
		return ActionUseTheSolar
	}

	// we don't need to change states
	return ActionNone
}
