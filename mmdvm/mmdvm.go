// Package mmdvm implements the MMDVM open-source Multi-Mode Digital Voice Modem
package mmdvm

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/tarm/serial"
)

const (
	GetVersion         uint8 = 0x00
	GetStatus          uint8 = 0x01
	SetConfig          uint8 = 0x02
	SetMode            uint8 = 0x03
	CalibrationData    uint8 = 0x08
	DStarHeader        uint8 = 0x10
	DStarData          uint8 = 0x11
	DStarLost          uint8 = 0x12
	DStarEOT           uint8 = 0x13
	DMRData            uint8 = 0x18
	DMRLost            uint8 = 0x19
	SetCACHShortLCData uint8 = 0x1a
	DMRTransmitStart   uint8 = 0x1b
	SystemFusionData   uint8 = 0x20
	SystemFusionLost   uint8 = 0x21
	ACK                uint8 = 0x70
	NAK                uint8 = 0x7f
	FrameStart         uint8 = 0xe0
)

// NAK reasons
const (
	InvalidCommand uint8 = iota + 1
	WrongMode
	CommandTooLong
	DataIncorrect
	NotEnoughBufferSpace
)

var (
	CommandName = map[uint8]string{
		GetVersion:         "get version",
		GetStatus:          "get status",
		SetConfig:          "set config",
		SetMode:            "set mode",
		CalibrationData:    "calibration data",
		DStarHeader:        "D-Star header",
		DStarData:          "D-Star data",
		DStarLost:          "D-Star lost",
		DStarEOT:           "D-Star End Of Transmission",
		DMRData:            "DMR data",
		DMRLost:            "DMR lost",
		SetCACHShortLCData: "set CACH short LC data",
		DMRTransmitStart:   "DMR transmit start",
		SystemFusionData:   "System Fusion data",
		SystemFusionLost:   "System Fusion lost",
		ACK:                "ACK",
		NAK:                "NAK",
	}
	DefaultTimeout = time.Second * 10
)

// Errors
var (
	ErrTimeout              = errors.New("mmdvm: timeout waiting for reply")
	ErrInvalidCommand       = errors.New("mmdvm: invalid command")
	ErrWrongMode            = errors.New("mmdvm: wrong mode")
	ErrCommandTooLong       = errors.New("mmdvm: command too long")
	ErrDataIncorrect        = errors.New("mmdvm: data incorrect")
	ErrNotEnoughBufferSpace = errors.New("mmdvm: not enough buffer space")
	nakError                = map[uint8]error{
		InvalidCommand:       ErrInvalidCommand,
		WrongMode:            ErrWrongMode,
		CommandTooLong:       ErrCommandTooLong,
		DataIncorrect:        ErrDataIncorrect,
		NotEnoughBufferSpace: ErrNotEnoughBufferSpace,
	}
)

// The package logger
var logger = *log.Logger

// States
const (
	StateIdle uint8 = iota
	StateDStar
	StateDMR
	StateSystemFusion
	StateCalibration uint8 = 99
)

// Flags
const (
	TXOn        uint8 = 0x01
	ADCOverflow uint8 = 0x02
)

// Inversions
const (
	InvertRXAudio uint8 = 1 << iota
	InvertTXAudio
	InvertTransmitOutput
)

// Fixed baudrate for the MMDVM modem is 115k2
const Baud = 115200

// Config holds the information retrieved by a Get Config command or sent by a Set Config command.
type Config struct {
	Inversion    uint8
	Modes        uint8
	TXDelay      uint8
	State        uint8
	RXInputLevel uint8
	TXInputLevel uint8
	DMRColorCode uint8
}

// Status holds the information retrieved by a Get Status command.
type Status struct {
	Modes                  uint8
	State                  uint8
	Flags                  uint8
	DStarBufferSize        uint8
	DMRTS1BufferSize       uint8
	DMRTS2BufferSize       uint8
	SystemFusionBufferSize uint8
}

type Modem struct {
	Config   *serial.Config
	Timeout  time.Duration
	Callback map[uint8]modem.ModemDataFunc

	port     *serial.Port
	callback map[uint8]chan []byte
	running  bool
	version  int
}

func New(config *serial.Config) (*Modem, error) {
	var err error

	m := &Modem{
		Config:   config,
		Callback: make(map[uint8]modem.ModemDataFunc),
		Timeout:  DefaultTimeout,
		callback: make(map[uint8]chan []byte),
	}
	m.Config.Baud = Baud

	return m, nil
}

func (m *Modem) Run() error {
	var (
		err  error
		data []byte
	)

	// Open the serial port
	logger.Printf("opening serial port %s at %d baud\n", m.Config.Name, m.Config.Baud)
	if m.port, err = serial.OpenPort(m.Config); err != nil {
		return err
	}

	// First we have to sync the modem, so we'll keep reading until we get an answer for our Get Version inquiry
	if _, err = m.port.Write([]byte{FrameStart, 0x03, GetVersion}); err != nil {
		return err
	}

	// Create a small buffer that fits the frame start, size and response byte
	data = make([]byte, 3)
	if _, err = m.port.Read(data); err != nil {
		return err
	}

	// Keep reading the next byte until we have an answer
	for data[0] != FrameStart && data[2] != GetVersion {
		// Shift our buffer and append a null byte, which we'll fill with data from the serial link
		data = append(data[1:], []byte{0x00}...)
		if _, err = m.port.Read(data[2:]); err != nil {
			return err
		}
	}

	// We are in a Get Version frame, check the received length
	if data[1] < 4 {
		return errors.New("mmdvm: synchronisation error")
	}

	// Receive the rest of the version information frame
	data = make([]byte, data[1]-3)
	if _, err = m.port.Read(data); err != nil {
		return err
	}

	m.version = int(data[0])
	if m.version != 0x01 {
		return fmt.Errorf("mmdvm: unsupported protocol version %d", m.version)
	}

	// Start receive loop
	m.running = true
	for m.running {
		// Read frame start and size byte
		data = make([]byte, 2)
		if _, err = m.port.Read(data); err != nil {
			return err
		}

		if data[0] != FrameStart {
			return m.errUnexpected(data[0], FrameStart)
		}
		if data[1] < 2 {
			return fmt.Errorf("mmdvm: received invalid packet length %d", data[1])
		}

		// Extend the receive buffer with the specified length
		data = append(data, make([]byte, data[1]-2)...)
		if _, err = m.Port.Read(data[2:]); err != nil {
			return err
		}

		switch data[2] {
		case ACK, NAK:
			// For ACK/NAK, the actual response type is in the next byte
			if len(data) < 4 {
				return fmt.Errorf("mmdvm: received invalid %s packet length %d", CommandName[data[2]], data[1])
			}

			// Check if there is a callback registered
			c, ok := m.callback[data[3]]
			if !ok {
				// No callback registered, log an error
				logger.Printf("received %s for unhandled command %#02x (%s)", CommandName[data[2]], data[3], CommandName[data[3]])
				continue
			}
			c <- data
			break

		case DStarHeader, DStarData, DMRData, SystemFusionData:
			// For these packets, we use callback functions
			if m.Callback[data[2]] == nil {
				logger.Printf("received %s but we have no callback registered (ignored)", CommandName[data[2]])
				continue
			}

			if err = m.Callback[data[2]](m, data); err != nil {
				return err
			}
			break

		default:
			if len(data) < 3 {
				return fmt.Errorf("mmdvm: received invalid packet length %d", data[1])
			}

			// Check if there is a callback registered
			c, ok := m.callback[data[2]]
			if !ok {
				logger.Printf("received unhandled response %#02x (%s)", data[2], CommandName[data[2]])
				continue
			}
			c <- data
			break
		}
	}

	return nil
}

func (m *Modem) errUnexpected(got, want uint8) error {
	if name, ok := CommandName[got]; ok {
		return fmt.Errorf("mmdvm: unexpected response, got %#02x (%s), wanted %#02x", got, name, want)
	}
	return fmt.Errorf("mmdvm: unexpected response, got %#02x, wanted %#02x", got, want)
}

func (m *Modem) send(body []byte) error {
	var size = uint8(len(body) + 2)
	var head = []byte{FrameStart, size}
	var frame = append(head, body...)
	_, err := m.port.Write(frame)
	return err
}

func (m *Modem) sendAndWait(body []byte, t time.Duration) ([]byte, error) {
	var command uint8

	switch body[0] {
	case ACK, NAK:
		command = body[1]
	default:
		command = body[0]
	}

	// Create our on-off receive channel ...
	m.callback[command] = make(chan []byte, 1)
	// ... and clean it up after this function is done
	defer func() {
		delete(m.callback, command)
	}()

	if err := m.send(body); err != nil {
		return nil, err
	}

	// Return once there is data on the channel or if there is a timeout
	select {
	case data := <-m.callback[command]:
		return data, nil
	case <-time.After(t):
		break
	}
	return nil, ErrTimeout
}

func (m *Modem) sendAndWaitForACK(body []byte, t time.Duration) error {
	data, err := m.sendAndWait(body, t)
	if err != nil {
		return err
	}

	switch data[2] {
	case ACK:
		return nil

	case NAK:
		if err, ok := nakError[data[4]]; ok {
			return err
		}
		return fmt.Errorf("mmdvm: received NAK for unknown reason %#02x", data[4])

	default:
		return m.errUnexpected(got, ACK)
	}
}

// SendDStarHeader sends a D-Star header, if there is an error it will be returned immediately, if the header was received correctly, no feedback will be provided
func (m *Modem) SendDStarHeader(head []byte, timeout time.Duration) error {
	return m.sendAndWaitForACK(append([]byte{DStarHeader}, head...), t)
}

// SendDStarData sends D-Star data, if there is an error it will be returned immediately, if the data was received correctly, no feedback will be provided
func (m *Modem) SendDStarData(data []byte, timeout time.Duration) error {
	return m.sendAndWaitForACK(append([]byte{DStarData}, data...), t)
}

// SendDStarEOT sends a D-Star End Of Transmission, if there is an error it will be returned immediately, if the data was received correctly, no feedback will be provided
func (m *Modem) SendDStarEOT(timeout time.Duration) error {
	return m.sendAndWaitForACK(append([]byte{DStarEOT}, data...), t)
}

// SendDMRData sends DMR data, if there is an error it will be returned immediately, if the data was received correctly, no feedback will be provided
func (m *Modem) SendDMRData(data []byte, timeout time.Duration) error {
	return m.sendAndWaitForACK(append([]byte{DMRData}, data...), t)
}

// SendSystemFusionData sends System Fusion data, if there is an error it will be returned immediately, if the data was received correctly, no feedback will be provided
func (m *Modem) SendSystemFusionData(data []byte, timeout time.Duration) error {
	return m.sendAndWaitForACK(append([]byte{SystemFusionData}, data...), t)
}

// SetConfig is used to inform the modem about parameters relevant to its operation
func (m *Modem) SetConfig(c Config) error {
	return m.sendAndWaitForACK([]byte{
		SetConfig,
		c.Inversion,
		c.Modes,
		c.TXDelay,
		c.State,
		c.RXInputLevel,
		c.TXInputLevel,
		c.DMRColorCode,
	}, m.Timeout)
}

// SetMode sets the supported modes
func (m *Modem) SetMode(mode uint8) error {
	return m.sendAndWaitForACK([]byte{
		SetMode,
		mode,
	}, m.Timeout)
}

// Status is used to determine the current parameters of the modem
func (m *Modem) Status() (*Status, error) {
	data, err := m.sendAndWait([]byte{GetStatus}, m.Timeout)
	if err != nil {
		return nil, err
	}
	if len(data) != 10 {
		return nil, fmt.Errorf("mmdvm: expected 10 status bytes, got %d", len(data))
	}

	return &Status{
		Modes:                  data[3],
		State:                  data[4],
		Flags:                  data[5],
		DStarBufferSize:        data[6],
		DMRTS1BufferSize:       data[7],
		DMRTS2BufferSize:       data[8],
		SystemFusionBufferSize: data[9],
	}, nil
}

// Version returns the modem version
func (m *Modem) Version() int {
	return m.version
}

func (m *Modem) SetDStarHeaderFunc(f ModemDataFunc) {
	m.Callback[DStarHeader] = f
}

func (m *Modem) SetDStarDataFunc(f ModemDataFunc) {
	m.Callback[DStarData] = f
}

func (m *Modem) SetDMRDataFunc(f ModemDataFunc) {
	m.Callback[DMRData] = f
}

func (m *Modem) SetSystemFusionDataFunc(f ModemDataFunc) {
	m.Callback[SystemFusionData] = f
}

func init() {
	logger = log.New(os.Stderr, "mmdvm: ", log.LstdFlags)
}
