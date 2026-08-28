//go:build darwin && cgo

package folderpicker

/*
#cgo LDFLAGS: -framework AppKit -framework Foundation
#include <stdlib.h>

char* nr_dropped_folders_json(void);
*/
import "C"

import (
	"context"
	"encoding/json"
	"fmt"
	"unsafe"
)

func dropped(ctx context.Context) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	value := C.nr_dropped_folders_json()
	if value == nil {
		return nil, nil
	}
	defer C.free(unsafe.Pointer(value))
	values := []string{}
	if err := json.Unmarshal([]byte(C.GoString(value)), &values); err != nil {
		return nil, fmt.Errorf("decode dropped folders: %w", err)
	}
	return values, nil
}
