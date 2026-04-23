package main

import (
	"github.com/cloudkucooland/go-daikin"
)

type CoolingAction int

const (
	ActionNone CoolingAction = iota
	ActionFullDeepCool
	ActionModerateNudge
	ActionRevertToSchedule
)

func evaluateCoolingAction(avgNetMW float64, info *daikin.Info, forecast *Forecast) CoolingAction {
	if info.Mode != daikin.ModeCool {
		return ActionNone
	}

	if info.OutdoorTemp < DeepCoolMinOutdoorTemp {
		if !info.ScheduleEnabled {
			return ActionRevertToSchedule
		}
		return ActionNone
	}

	if forecast != nil && forecast.Low < DeepCoolOverrideNightLowTemp {
		if !info.ScheduleEnabled {
			return ActionRevertToSchedule
		}
		return ActionNone
	}

	switch {
	case avgNetMW < DeepCoolMinExportWatts:
		if info.ScheduleEnabled || info.CoolSetpoint != DeepCoolTemp {
			return ActionFullDeepCool
		}
	case avgNetMW < DeepCoolModerateExportWatts:
		if info.ScheduleEnabled {
			return ActionModerateNudge
		}
	case avgNetMW > DeepCoolMaxImportWatts:
		if !info.ScheduleEnabled {
			return ActionRevertToSchedule
		}
	}

	return ActionNone
}
