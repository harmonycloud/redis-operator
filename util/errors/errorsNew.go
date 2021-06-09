package errors

import (
	"encoding/json"
	"fmt"
)

type Err struct {
	Code int
	Msg  string
}

func (e *Err) Error() string {
	err, _ := json.Marshal(e)
	return string(err)
}

// 保证err正常，否则panic
func EnsureNormal(err error) {
	if err != nil {
		panic(err)
	}
}

func EnsureNormalMsgf(err error, format string, a ...interface{}) {
	if err != nil {
		panic(err)
	}
}

func New(code int, format string, a ...interface{}) *Err {
	msg := fmt.Sprintf(format, a)
	if len(a) == 0 {
		msg = fmt.Sprint(format, a)
	}
	return &Err{
		Code: code,
		Msg:  msg,
	}
}

func NewErr(err error) *Err {
	return &Err{
		Code: ERR_UNKNOW,
		Msg:  err.Error(),
	}
}
func NewErrMsg(err string) *Err {
	return &Err{
		Code: ERR_UNKNOW,
		Msg:  err,
	}
}

func NewErrfMsgf(format string, a ...interface{}) *Err {
	msg := fmt.Sprintf(format, a)
	if len(a) == 0 {
		msg = fmt.Sprint(format, a)
	}
	return &Err{
		Code: ERR_UNKNOW,
		Msg:  msg,
	}
}

const ERR_NO_MASTER = 401
const ERR_SYSTEM = 404
const ERR_UNKNOW = -1
