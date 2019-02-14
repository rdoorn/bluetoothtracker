package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"strings"
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
	Addr    ble.Addr
	RSSI    int
	Name    string
	Company string
	Type    string
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

	fmt.Printf("Summary:\n")
	for id, dev := range deviceList.Devices {
		fmt.Printf("Device: %d Found: %+v\n", id, dev)
	}
}

func (l *DeviceList) poller() {
	timeout := time.NewTimer(*du).C

	for {
		select {
		case <-timeout:
			return
		default:
			l.scan()
			l.query()
		}
	}
}

func (d *DeviceList) scan() {
	fmt.Printf("Scanning...\n")
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 2*time.Second))
	chkErr(ble.Scan(ctx, *dup, d.scanHandler, nil))
}

func (d *DeviceList) query() {
	for _, dev := range d.Devices {
		if dev.Name == "" {
			fmt.Printf("Querying %s...\n", dev.Addr.String())
			//d.queryHandler(id)
			caller(dev.Addr.String())
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
	_, new := d.new(a.Addr())
	if new {
		fmt.Printf("New device found [%s] C %3d\n", a.Addr(), a.RSSI())
	}
}

func (d *DeviceList) queryHandler(id int) {

	filter := func(af ble.Advertisement) bool {
		return strings.ToUpper(af.Addr().String()) == strings.ToUpper(d.Devices[id].Addr.String())
	}

	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 2*time.Second))
	cln, err := ble.Connect(ctx, filter)
	if err != nil {
		log.Printf("can't connect : %s", err)
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
		log.Fatalf("can't discover profile: %s", err)
	}
	// Start the exploration.
	explore(cln, p, d.Devices[id])

	// Disconnect the connection. (On OS X, this might take a while.)
	fmt.Printf("Disconnecting [ %s ]... (this might take up to few seconds on OS X)\n", cln.Addr())
	cln.CancelConnection()

	<-done
}

// we already know this device, don't poll again

/*
	if a.Connectable() {
		fmt.Printf("[%s] C %3d:", a.Addr(), a.RSSI())
	} else {
		fmt.Printf("[%s] N %3d:", a.Addr(), a.RSSI())
	}
	comma := ""
	if len(a.LocalName()) > 0 {
		fmt.Printf(" Name: %s", a.LocalName())
		comma = ","
	}
	if len(a.Services()) > 0 {
		fmt.Printf("%s Svcs: %v", comma, a.Services())
		comma = ","
	}
	fmt.Printf(" TXLevel: %3d", a.TxPowerLevel())
	fmt.Printf(" Connectable: %t", a.Connectable())
	if len(a.ManufacturerData()) > 0 {
		fmt.Printf("%s MD: %X", comma, a.ManufacturerData())
	}
	fmt.Printf("\n")
*/
//}

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

			/*
				if *sub != 0 {
					// Don't bother to subscribe the Service Changed characteristics.
					if c.UUID.Equal(ble.ServiceChangedUUID) {
						continue
					}

					// Don't touch the Apple-specific Service/Characteristic.
					// Service: D0611E78BBB44591A5F8487910AE4366
					// Characteristic: 8667556C9A374C9184ED54EE27D90049, Property: 0x18 (WN),
					//   Descriptor: 2902, Client Characteristic Configuration
					//   Value         0000 | "\x00\x00"
					if c.UUID.Equal(ble.MustParse("8667556C9A374C9184ED54EE27D90049")) {
						continue
					}

					if (c.Property & ble.CharNotify) != 0 {
						fmt.Printf("\n-- Subscribe to notification for %s --\n", *sub)
						h := func(req []byte) { fmt.Printf("Notified: %q [ % X ]\n", string(req), req) }
						if err := cln.Subscribe(c, false, h); err != nil {
							log.Fatalf("subscribe failed: %s", err)
						}
						time.Sleep(*sub)
						if err := cln.Unsubscribe(c, false); err != nil {
							log.Fatalf("unsubscribe failed: %s", err)
						}
						fmt.Printf("-- Unsubscribe to notification --\n")
					}
					if (c.Property & ble.CharIndicate) != 0 {
						fmt.Printf("\n-- Subscribe to indication of %s --\n", *sub)
						h := func(req []byte) { fmt.Printf("Indicated: %q [ % X ]\n", string(req), req) }
						if err := cln.Subscribe(c, true, h); err != nil {
							log.Fatalf("subscribe failed: %s", err)
						}
						time.Sleep(*sub)
						if err := cln.Unsubscribe(c, true); err != nil {
							log.Fatalf("unsubscribe failed: %s", err)
						}
						fmt.Printf("-- Unsubscribe to indication --\n")
					}
				}
			*/
		}
		fmt.Printf("\n")
	}
	return nil
}

func caller(addr string) {
	filter := func(a ble.Advertisement) bool {
		return strings.ToUpper(a.Addr().String()) == strings.ToUpper(addr)
	}

	// Scan for specified durantion, or until interrupted by user.
	fmt.Printf("Querying for %s...\n")
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 2*time.Second))
	cln, err := ble.Connect(ctx, filter)
	if err != nil {
		log.Fatalf("can't connect : %s", err)
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
		log.Fatalf("can't discover profile: %s", err)
	}

	// Start the exploration.
	explore2(cln, p)

	// Disconnect the connection. (On OS X, this might take a while.)
	fmt.Printf("Disconnecting [ %s ]... (this might take up to few seconds on OS X)\n", cln.Addr())
	cln.CancelConnection()

	<-done
}

func explore2(cln ble.Client, p *ble.Profile) error {
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

			/*
				if *sub != 0 {
					// Don't bother to subscribe the Service Changed characteristics.
					if c.UUID.Equal(ble.ServiceChangedUUID) {
						continue
					}

					// Don't touch the Apple-specific Service/Characteristic.
					// Service: D0611E78BBB44591A5F8487910AE4366
					// Characteristic: 8667556C9A374C9184ED54EE27D90049, Property: 0x18 (WN),
					//   Descriptor: 2902, Client Characteristic Configuration
					//   Value         0000 | "\x00\x00"
					if c.UUID.Equal(ble.MustParse("8667556C9A374C9184ED54EE27D90049")) {
						continue
					}

					if (c.Property & ble.CharNotify) != 0 {
						fmt.Printf("\n-- Subscribe to notification for %s --\n", *sub)
						h := func(req []byte) { fmt.Printf("Notified: %q [ % X ]\n", string(req), req) }
						if err := cln.Subscribe(c, false, h); err != nil {
							log.Fatalf("subscribe failed: %s", err)
						}
						time.Sleep(*sub)
						if err := cln.Unsubscribe(c, false); err != nil {
							log.Fatalf("unsubscribe failed: %s", err)
						}
						fmt.Printf("-- Unsubscribe to notification --\n")
					}
					if (c.Property & ble.CharIndicate) != 0 {
						fmt.Printf("\n-- Subscribe to indication of %s --\n", *sub)
						h := func(req []byte) { fmt.Printf("Indicated: %q [ % X ]\n", string(req), req) }
						if err := cln.Subscribe(c, true, h); err != nil {
							log.Fatalf("subscribe failed: %s", err)
						}
						time.Sleep(*sub)
						if err := cln.Unsubscribe(c, true); err != nil {
							log.Fatalf("unsubscribe failed: %s", err)
						}
						fmt.Printf("-- Unsubscribe to indication --\n")
					}
				}
			*/
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
		fmt.Printf("done\n")
	case context.Canceled:
		fmt.Printf("canceled\n")
	default:
		log.Fatalf(err.Error())
	}
}
