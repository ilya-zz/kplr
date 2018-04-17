package kplr

import (
	"fmt"
	"os"
	"strings"

	"github.com/jrivets/gorivets"
	"github.com/jrivets/log4g"
)

var (
	DefaultLogger = log4g.GetLogger("kplr")

	ErrNotFound = fmt.Errorf("not found")
)

func Assert(cond bool, errMsg string) {
	if !cond {
		DefaultLogger.Fatal("ASSERTION ERROR: ", errMsg)
		panic(errMsg)
	}
}

func GetBoolPtr(val bool) *bool {
	return &val
}

func GetBoolVal(ptr *bool, defVal bool) bool {
	if ptr != nil {
		return *ptr
	}
	return defVal
}

func GetIntPtr(val int) *int {
	return &val
}

func GetIntVal(ptr *int, defVal int) int {
	if ptr != nil {
		return *ptr
	}
	return defVal
}

func GetInt64Val(ptr *int64, defVal int64) int64 {
	if ptr != nil {
		return *ptr
	}
	return defVal
}

func GetStringVal(ptr *string, defVal string) string {
	if ptr != nil {
		return *ptr
	}
	return defVal
}

func GetStringPtr(val string) *string {
	if val == "" {
		return nil
	}
	return &val
}

func IsFileNotExist(filename string) bool {
	_, err := os.Stat(filename)
	return os.IsNotExist(err)
}

// FormatSize prints the size by scale 1000, ex: 23Kb(23450)
func FormatSize(val int64) string {
	if val < 1000 {
		return fmt.Sprint(val)
	}
	return fmt.Sprint(gorivets.FormatInt64(val, 1000), "(", val, ")")
}

func FormatProgress(size int, perc float64) string {
	fl := int(float64(size-2) * perc / 100.0)
	empt := size - 2 - fl
	return fmt.Sprintf("%5.2f%% |%s%s|", perc, strings.Repeat("#", fl), strings.Repeat("-", empt))
}
