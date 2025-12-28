package ffi

/*
#cgo CFLAGS: -I${SRCDIR}/../../rust/txbuild/include
#cgo LDFLAGS: -L${SRCDIR}/../../rust/txbuild/target/release -ljuno_txbuild

#include "juno_txbuild.h"
#include <stdlib.h>
*/
import "C"

import (
	"errors"
	"unsafe"
)

var errNull = errors.New("ffi: null response")

func OrchardWitnessJSON(reqJSON string) (string, error) {
	cReq := C.CString(reqJSON)
	defer C.free(unsafe.Pointer(cReq))

	out := C.juno_txbuild_orchard_witness_json(cReq)
	if out == nil {
		return "", errNull
	}
	defer C.juno_txbuild_string_free(out)

	return C.GoString(out), nil
}
