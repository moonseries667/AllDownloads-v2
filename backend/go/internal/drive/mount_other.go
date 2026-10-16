//go:build !windows

package drive

func observeMount() (mountObservation, error) {
	return mountObservation{}, fault("MOUNT_PLATFORM_UNSUPPORTED")
}
func checkWinFsp() error { return fault("MOUNT_PLATFORM_UNSUPPORTED") }
