package gobackend

import (
	"fmt"

	"github.com/dop251/goja"
)

func gojaValueIsEmpty(value goja.Value) bool {
	return value == nil || goja.IsUndefined(value) || goja.IsNull(value)
}

func gojaArrayLength(value goja.Value, vm *goja.Runtime) (int, error) {
	if gojaValueIsEmpty(value) {
		return 0, nil
	}
	lengthValue := value.ToObject(vm).Get("length")
	if gojaValueIsEmpty(lengthValue) {
		return 0, fmt.Errorf("value is not an array")
	}
	length := lengthValue.ToInteger()
	if length <= 0 {
		return 0, nil
	}
	return int(length), nil
}
