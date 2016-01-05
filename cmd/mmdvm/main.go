package main

import (
	"flag"
	"fmt"
	"log"
	"strings"

	"github.com/pd0mz/go-dv/mmdvm"
	"github.com/tarm/serial"
)

func main() {
	port := flag.String("port", "/dev/cu.usbmodem1411", "Modem port")
	flag.Parse()

	modem := mmdvm.New(&serial.Config{Name: *port})
	if err := modem.Sync(); err != nil {
		log.Fatalf("error syncing MMDVM modem on %s: %v", *port, err)
	}

	go modem.Run()
	defer modem.Close()

	status, err := modem.Status()
	if err != nil {
		log.Fatalf("error retrieving modem status: %v", err)
	}
	fmt.Printf("modes: %d (", status.Modes)
	modes := []string{}
	if status.Modes&mmdvm.ModeDStar > 0 {
		modes = append(modes, "D-Star")
	}
	if status.Modes&mmdvm.ModeDMR > 0 {
		modes = append(modes, "DMR")
	}
	if status.Modes&mmdvm.ModeSystemFusion > 0 {
		modes = append(modes, "System Fusion")
	}
	if len(modes) == 0 {
		fmt.Println("none)")
	} else {
		fmt.Printf("%s)\n", strings.Join(modes, ","))
	}
	fmt.Printf("state: %d (", status.State)
	switch status.State {
	case mmdvm.StateIdle:
		fmt.Println("idle)")
		break
	case mmdvm.StateDStar:
		fmt.Println("d-star)")
		break
	case mmdvm.StateDMR:
		fmt.Println("DMR)")
		break
	case mmdvm.StateSystemFusion:
		fmt.Println("System Fusion)")
		break
	case mmdvm.StateCalibration:
		fmt.Println("calibration)")
		break
	default:
		fmt.Println("unknown)")
	}
	fmt.Printf("flags: %d\n", status.Flags)
	fmt.Println("buffer sizes:")
	fmt.Printf("  dstar..: %d\n", status.DStarBufferSize)
	fmt.Printf("  DMR TS1: %d\n", status.DMRTS1BufferSize)
	fmt.Printf("  DMR TS2: %d\n", status.DMRTS2BufferSize)
	fmt.Printf("  sfusion: %d\n", status.SystemFusionBufferSize)
}
