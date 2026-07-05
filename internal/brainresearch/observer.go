package brainresearch

import "reflect"

func emitEvent(observer Observer, name string, data map[string]interface{}) {
	if observerIsNil(observer) {
		return
	}
	observer.Event(name, data)
}

func emitPlannerInput(observer Observer, attempt string, input string) {
	if observerIsNil(observer) {
		return
	}
	attemptAware := false
	if attempt != "" {
		if attemptObserver, ok := observer.(AttemptPlannerInputObserver); ok {
			attemptAware = true
			attemptObserver.PlannerInputAttempt(attempt, input)
		}
	}
	if attempt != "" && attempt != "initial" && attemptAware {
		return
	}
	observer.PlannerInput(input)
}

func emitPlannerOutput(observer Observer, attempt string, output string) {
	if observerIsNil(observer) {
		return
	}
	attemptAware := false
	if attempt != "" {
		if attemptObserver, ok := observer.(AttemptPlannerOutputObserver); ok {
			attemptAware = true
			attemptObserver.PlannerOutputAttempt(attempt, output)
		}
	}
	if attempt != "" && attempt != "initial" && attemptAware {
		return
	}
	observer.PlannerOutput(output)
}

func observerIsNil(observer Observer) bool {
	if observer == nil {
		return true
	}
	value := reflect.ValueOf(observer)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
