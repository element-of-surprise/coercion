// Code generated by "stringer -type=ErrCode"; DO NOT EDIT.

package plugins

import "strconv"

func _() {
	// An "invalid array index" compiler error signifies that the constant values have changed.
	// Re-run the stringer command to generate them again.
	var x [1]struct{}
	_ = x[ECUnknown-0]
}

const _ErrCode_name = "ECUnknown"

var _ErrCode_index = [...]uint8{0, 9}

func (i ErrCode) String() string {
	if i >= ErrCode(len(_ErrCode_index)-1) {
		return "ErrCode(" + strconv.FormatInt(int64(i), 10) + ")"
	}
	return _ErrCode_name[_ErrCode_index[i]:_ErrCode_index[i+1]]
}
