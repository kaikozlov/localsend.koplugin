package models

import (
	"encoding/json"
	"math"
	"strconv"
)

// FlexInt is an int64 that accepts both JSON integers and floats during
// unmarshalling. Some non-standard LocalSend clients serialize file sizes
// as floats (e.g., 346.0 instead of 346). Go's default JSON decoder rejects
// floats for int64 fields, causing a 400 response. FlexInt truncates the
// fractional part and stores the integer value.
type FlexInt int64

// UnmarshalJSON accepts integers, floats, and numeric strings.
func (f *FlexInt) UnmarshalJSON(data []byte) error {
	// Try integer first (the common case — no allocation)
	if data[0] != '"' && data[0] != '.' {
		var i int64
		if err := json.Unmarshal(data, &i); err == nil {
			*f = FlexInt(i)
			return nil
		}
		// Not a pure integer — try float
		var fl float64
		if err := json.Unmarshal(data, &fl); err == nil {
			*f = FlexInt(int64(fl))
			return nil
		}
	}

	// Try quoted numeric string (e.g., "346" or "346.0")
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		// Try integer parse first
		if i, err := strconv.ParseInt(s, 10, 64); err == nil {
			*f = FlexInt(i)
			return nil
		}
		// Try float parse
		if fl, err := strconv.ParseFloat(s, 64); err == nil {
			*f = FlexInt(int64(fl))
			return nil
		}
	}

	// Give up — try float one more time as catch-all
	var fl float64
	if err := json.Unmarshal(data, &fl); err != nil {
		return err
	}
	*f = FlexInt(int64(fl))
	return nil
}

// MarshalJSON serializes as a plain integer (no fractional part).
func (f FlexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(f))
}

// Int64 returns the underlying int64 value.
func (f FlexInt) Int64() int64 {
	return int64(f)
}

// Valid reports whether the FlexInt is non-negative (a valid file size).
func (f FlexInt) Valid() bool {
	return int64(f) >= 0 && int64(f) <= math.MaxInt64
}
