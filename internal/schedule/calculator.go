package schedule

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

var clockPattern = regexp.MustCompile(`^(\d{2}):(\d{2})$`)

var ErrEqualBoundaries = errors.New("enable and disable times must differ")

type DailyWindow struct {
	location      *time.Location
	enableSecond  int
	disableSecond int
}

type DailyState struct {
	Enabled           bool
	NextTransition    time.Time
	NextTargetEnabled bool
}

func NewDailyWindow(location *time.Location, enableTime, disableTime string) (DailyWindow, error) {
	if location == nil {
		return DailyWindow{}, errors.New("timezone is required")
	}

	enableSecond, err := parseClock(enableTime)
	if err != nil {
		return DailyWindow{}, fmt.Errorf("enable time: %w", err)
	}
	disableSecond, err := parseClock(disableTime)
	if err != nil {
		return DailyWindow{}, fmt.Errorf("disable time: %w", err)
	}
	if enableSecond == disableSecond {
		return DailyWindow{}, ErrEqualBoundaries
	}

	return DailyWindow{
		location:      location,
		enableSecond:  enableSecond,
		disableSecond: disableSecond,
	}, nil
}

func (w DailyWindow) StateAt(now time.Time) DailyState {
	localNow := now.In(w.location)
	nowSecond := localNow.Hour()*60*60 + localNow.Minute()*60 + localNow.Second()
	base := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, w.location)

	if w.enableSecond < w.disableSecond {
		switch {
		case nowSecond < w.enableSecond:
			return DailyState{NextTransition: atSecond(base, w.enableSecond), NextTargetEnabled: true}
		case nowSecond < w.disableSecond:
			return DailyState{Enabled: true, NextTransition: atSecond(base, w.disableSecond)}
		default:
			return DailyState{NextTransition: atSecond(base.AddDate(0, 0, 1), w.enableSecond), NextTargetEnabled: true}
		}
	}

	switch {
	case nowSecond < w.disableSecond:
		return DailyState{Enabled: true, NextTransition: atSecond(base, w.disableSecond)}
	case nowSecond < w.enableSecond:
		return DailyState{NextTransition: atSecond(base, w.enableSecond), NextTargetEnabled: true}
	default:
		return DailyState{Enabled: true, NextTransition: atSecond(base.AddDate(0, 0, 1), w.disableSecond)}
	}
}

func (w DailyWindow) EnableSecond() int {
	return w.enableSecond
}

func (w DailyWindow) DisableSecond() int {
	return w.disableSecond
}

func (w DailyWindow) EnableTime() string {
	return formatClock(w.enableSecond)
}

func (w DailyWindow) DisableTime() string {
	return formatClock(w.disableSecond)
}

func parseClock(value string) (int, error) {
	matches := clockPattern.FindStringSubmatch(value)
	if matches == nil {
		return 0, errors.New("must use HH:MM format")
	}

	hour, _ := strconv.Atoi(matches[1])
	minute, _ := strconv.Atoi(matches[2])
	if hour > 23 || minute > 59 {
		return 0, errors.New("must be a valid 24-hour clock time")
	}
	return hour*60*60 + minute*60, nil
}

func formatClock(second int) string {
	return fmt.Sprintf("%02d:%02d", second/3600, second%3600/60)
}

func atSecond(day time.Time, second int) time.Time {
	return day.Add(time.Duration(second) * time.Second)
}
