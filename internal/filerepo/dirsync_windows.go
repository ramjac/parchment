//go:build windows

package filerepo

func syncDirectory(string) error {
	// Windows does not support flushing directory handles through os.File.Sync.
	return nil
}
