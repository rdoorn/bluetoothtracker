package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/pkg/errors"
)

var (
	device = flag.String("device", "default", "implementation of ble")
	du     = flag.Duration("du", 30*time.Second, "scanning duration")
	dup    = flag.Bool("dup", true, "allow duplicate reported")
)

type Device struct {
	Addr         ble.Addr
	RSSI         int
	Name         string
	Company      string
	Type         string
	TXPowerLevel int
}

type DeviceList struct {
	Devices []*Device
}

func main() {
	flag.Parse()

	deviceList := &DeviceList{}

	d, err := dev.NewDevice(*device)
	if err != nil {
		log.Fatalf("can't connect to BT interface : %s", err)
	}
	ble.SetDefaultDevice(d)

	deviceList.poller()

}

func (l *DeviceList) status() {
	for id, dev := range l.Devices {
		fmt.Printf("Device: %d Found: %+v\n", id, dev)
	}
}

func (l *DeviceList) poller() {
	timeout := time.NewTimer(*du).C
	ticker := time.NewTicker(10 * time.Second).C

	for {
		select {
		case <-timeout:
			return
		case <-ticker:
			l.status()
		default:
			l.scan()
			l.query()
		}
	}
}

func (d *DeviceList) scan() {
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 2*time.Second))
	chkErr(ble.Scan(ctx, *dup, d.scanHandler, nil))
}

func (d *DeviceList) query() {
	for id, dev := range d.Devices {
		if dev.Name == "" {
			d.queryHandler(id)
		}
	}
}

func (d *DeviceList) new(addr ble.Addr) (*Device, bool) {
	for _, dev := range d.Devices {
		if dev.Addr.String() == addr.String() {
			return nil, false
		}
	}
	new := &Device{
		Addr: addr,
	}
	d.Devices = append(d.Devices, new)
	return new, true
}

func (d *DeviceList) scanHandler(a ble.Advertisement) {
	device, new := d.new(a.Addr())
	if new {
		fmt.Printf("New device found [%s] C %3d\n", a.Addr(), a.RSSI())
		device.TXPowerLevel = a.TxPowerLevel()
		device.RSSI = a.RSSI()
	}
}

func (d *DeviceList) queryHandler(id int) {

	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 10*time.Second))

	cln, err := ble.Dial(ctx, d.Devices[id].Addr)
	if err != nil {
		log.Printf("can't Dial : %s", err)
		return
	}

	// Make sure we had the chance to print out the message.
	done := make(chan struct{})
	// Normally, the connection is disconnected by us after our exploration.
	// However, it can be asynchronously disconnected by the remote peripheral.
	// So we wait(detect) the disconnection in the go routine.
	go func() {
		<-cln.Disconnected()
		fmt.Printf("[ %s ] is disconnected \n", cln.Addr())
		close(done)
	}()

	fmt.Printf("Discovering profile...\n")
	p, err := cln.DiscoverProfile(true)
	if err != nil {
		log.Printf("can't discover profile: %s", err)
		return
	}
	// Start the exploration.
	explore(cln, p, d.Devices[id])

	// Disconnect the connection. (On OS X, this might take a while.)
	fmt.Printf("Disconnecting [ %s ]... (this might take up to few seconds on OS X)\n", cln.Addr())
	cln.CancelConnection()

	<-done
}

func explore(cln ble.Client, p *ble.Profile, d *Device) error {
	for _, s := range p.Services {
		fmt.Printf("    Service: %s %s, Handle (0x%02X)\n", s.UUID, ble.Name(s.UUID), s.Handle)

		for _, c := range s.Characteristics {

			fmt.Printf("      Characteristic: %s %s, Property: 0x%02X (%s), Handle(0x%02X), VHandle(0x%02X)\n",
				c.UUID, ble.Name(c.UUID), c.Property, propString(c.Property), c.Handle, c.ValueHandle)
			if (c.Property & ble.CharRead) != 0 {
				b, err := cln.ReadCharacteristic(c)
				if err != nil {
					fmt.Printf("Failed to read characteristic: %s\n", err)
					continue
				}
				fmt.Printf("        Value         %x | %q\n", b, b)
				if c.UUID.String() == "2a00" { // device name
					d.Name = string(b)
				}
				if c.UUID.String() == "2a29" { // device name
					d.Company = string(b)
				}
				if c.UUID.String() == "2a24" { // device name
					d.Type = string(b)
				}
			}

			for _, d := range c.Descriptors {
				fmt.Printf("        Descriptor: %s %s, Handle(0x%02x)\n", d.UUID, ble.Name(d.UUID), d.Handle)
				b, err := cln.ReadDescriptor(d)
				if err != nil {
					fmt.Printf("Failed to read descriptor: %s\n", err)
					continue
				}
				fmt.Printf("        Value         %x | %q\n", b, b)
			}
		}
		fmt.Printf("\n")
	}
	return nil
}

func propString(p ble.Property) string {
	var s string
	for k, v := range map[ble.Property]string{
		ble.CharBroadcast:   "B",
		ble.CharRead:        "R",
		ble.CharWriteNR:     "w",
		ble.CharWrite:       "W",
		ble.CharNotify:      "N",
		ble.CharIndicate:    "I",
		ble.CharSignedWrite: "S",
		ble.CharExtended:    "E",
	} {
		if p&k != 0 {
			s += v
		}
	}
	return s
}

func chkErr(err error) {
	switch errors.Cause(err) {
	case nil:
	case context.DeadlineExceeded:
		// fmt.Printf("done\n")
	case context.Canceled:
		fmt.Printf("canceled\n")
	default:
		log.Fatalf(err.Error())
	}
}
