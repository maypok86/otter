package parser

const (
	ArcFormat            = "arc"
	LirsFormat           = "lirs"
	OracleGeneralFormat  = "oracleGeneral"
	LibcachesimCSVFormat = "libcachesimCSV"
	ScarabFormat         = "scarab"
	CordaFormat          = "corda"
)

func IsAvailableFormat(format string) bool {
	switch format {
	case ArcFormat, LirsFormat, OracleGeneralFormat, LibcachesimCSVFormat, ScarabFormat, CordaFormat:
		return true
	default:
		return false
	}
}
