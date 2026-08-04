//go:build !darwin && !linux

package lima

func migrationSeekData(int, int64) (int64, error) {
	return 0, errMigrationSparseSeekUnsupported
}

func migrationSeekHole(int, int64) (int64, error) {
	return 0, errMigrationSparseSeekUnsupported
}

func migrationSeekNoMoreData(error) bool { return false }
