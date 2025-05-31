package parser

const (
	ArcFormat            = "arc"
	LirsFormat           = "lirs"
	OracleGeneralFormat  = "oracleGeneral"
	LibcachesimCSVFormat = "libcachesimCSV"
)

func IsAvailableFormat(format string) bool {
	switch format {
	case ArcFormat, LirsFormat, OracleGeneralFormat, LibcachesimCSVFormat:
		return true
	default:
		return false
	}
}
