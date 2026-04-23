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

	// Use setpoint as a proxy for manual override if schedule reports enabled but we are at the floor.
	isManual := !info.ScheduleEnabled || info.CoolSetpoint == DeepCoolTemp

	if info.OutdoorTemp < DeepCoolMinOutdoorTemp {
		if isManual {
			return ActionRevertToSchedule
		}
		return ActionNone
	}

	if forecast != nil && forecast.Low < DeepCoolOverrideNightLowTemp {
		if isManual {
			return ActionRevertToSchedule
		}
		return ActionNone
	}

	switch {
	case avgNetMW < DeepCoolMinExportWatts:
		if !isManual || info.CoolSetpoint != DeepCoolTemp {
			return ActionFullDeepCool
		}
	case avgNetMW < DeepCoolModerateExportWatts:
		// If we are on schedule, we want to nudge.
		// If we are already manual (e.g., Full Deep Cool), we want to "relax" to a nudge to avoid over-cooling.
		if !isManual || info.CoolSetpoint == DeepCoolTemp {
			return ActionModerateNudge
		}
	case avgNetMW > DeepCoolMaxImportWatts:
		if isManual {
			return ActionRevertToSchedule
		}
	}

	return ActionNone
}
