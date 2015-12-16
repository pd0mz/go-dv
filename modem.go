package dv

// Modes
const (
	ModeDStar uint8 = 1 << iota
	ModeDMR
	ModeSystemFusion
)

// ModemDataFunc is the callback function for receiving D-Star headers and data, DMR data or System Fusion data
type ModemDataFunc func(Modem, []byte) error

// Modem describes the interface for digital voice modems
type Modem interface {
	// Modes reports what modes are supported by the modem
	Modes() uint8

	// Close stops communications with the modem
	Close() error

	// Run starts communications with the modem
	Run() error

	// Version returns the modem version
	Version() int

	// Functions to update modem callbacks
	SetDStarHeaderFunc(ModemDataFunc)
	SetDStarDataFunc(ModemDataFunc)
	SetDMRDataFunc(ModemDataFunc)
	SetSystemFusionDataFunc(ModemDataFunc)
}
