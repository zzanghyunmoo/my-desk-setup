package safefile

import (
	"errors"
	"fmt"
	"io"
)

// ReadRegularNoFollow snapshots one bounded regular file through the same
// no-follow handle used to inspect its size.
func ReadRegularNoFollow(path string, maximumBytes int64) (
	data []byte,
	returnErr error,
) {
	if maximumBytes < 0 {
		return nil, errors.New("maximum file size must not be negative")
	}
	file, size, err := OpenRegularNoFollow(path)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := file.Close(); returnErr == nil && err != nil {
			data = nil
			returnErr = fmt.Errorf("close regular file: %w", err)
		}
	}()
	if size < 0 || size > maximumBytes {
		return nil, errors.New("regular file exceeds the size limit")
	}
	data, err = io.ReadAll(io.LimitReader(file, maximumBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read regular file: %w", err)
	}
	if int64(len(data)) != size || int64(len(data)) > maximumBytes {
		return nil, errors.New("regular file changed while it was read")
	}
	return data, nil
}
