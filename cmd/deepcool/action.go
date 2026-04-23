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

func evaluateCoolingAction(avgNetMW float64, info *daikin.Info, forecast *Forecast, isActiveControl bool) CoolingAction {
	// If the utility has taken control, we treat it as manual.
	// We also respect the API's override flag if it ever starts working.
	isManual := isActiveControl || info.SchedOverride != 0

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
		// forecast says tonight will be cool, no need to spend the solar, get a few pennies from the power company
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
