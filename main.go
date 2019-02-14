package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/go-ble/ble"
	"github.com/go-ble/ble/examples/lib/dev"
	"github.com/pkg/errors"
	"github.com/rdoorn/kalmanfilter"
)

type Device struct {
	Addr         ble.Addr
	RSSI         int
	Name         string
	Company      string
	Type         string
	TXPowerLevel int
	lastseen     time.Time
	lastquery    time.Time
	kf           *kalmanfilter.Filter
	state        float64
}

type DeviceList struct {
	Devices []*Device
	m       sync.Mutex
}

func main() {

	deviceList := &DeviceList{}

	d, err := dev.NewDevice("default")
	if err != nil {
		log.Fatalf("can't connect to BT interface : %s", err)
	}
	ble.SetDefaultDevice(d)

	deviceList.poller()

}

func (l *DeviceList) status() {
	l.m.Lock()
	defer l.m.Unlock()
	for id, dev := range l.Devices {
		if time.Now().Sub(dev.lastseen) < 120*time.Second {
			dist := distance(float64(dev.RSSI))
			distKalman := distance(float64(dev.state))
			fmt.Printf("Device: %d details: %+v distance: %f kfk: %f\n", id, dev, dist, distKalman)
		}
	}
}

func (l *DeviceList) poller() {
	// wait for sigint or sigterm for cleanup - note that sigterm cannot be caught
	sigterm := make(chan os.Signal, 10)
	signal.Notify(sigterm, os.Interrupt, syscall.SIGTERM)

	ticker := time.NewTicker(10 * time.Second).C

	for {
		select {
		case <-sigterm:
			return
		case <-ticker:
			l.status()
		default:
			l.scan()
			l.query()
		}
	}
}

func (l *DeviceList) scan() {
	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 2*time.Second))
	chkErr(ble.Scan(ctx, false, l.scanHandler, nil))
}

func (l *DeviceList) query() {
	for id, dev := range l.Devices {
		if dev.Name == "" {
			if time.Now().Sub(dev.lastquery) > 60*time.Second {
				l.queryHandler(id)
				l.Devices[id].lastquery = time.Now()
			}
		}
	}
}

func (l *DeviceList) new(addr ble.Addr) (*Device, bool) {
	l.m.Lock()
	defer l.m.Unlock()
	for id, dev := range l.Devices {
		if dev.Addr.String() == addr.String() {
			return l.Devices[id], false
		}
	}
	new := &Device{
		Addr:     addr,
		kf:       kalmanfilter.New(0.01, 0.75),
		lastseen: time.Now(),
	}
	l.Devices = append(l.Devices, new)
	return new, true
}

func (l *DeviceList) scanHandler(a ble.Advertisement) {
	device, new := l.new(a.Addr())
	if new {
		fmt.Printf("New device found [%s] C %3d\n", a.Addr(), a.RSSI())
	}
	// update signal strength

	device.RSSI = a.RSSI()
	device.TXPowerLevel = a.TxPowerLevel()

	device.state = device.kf.Filter(float64(device.RSSI))
}

func (l *DeviceList) queryHandler(id int) {

	ctx := ble.WithSigHandler(context.WithTimeout(context.Background(), 20*time.Second))
	log.Print("Quering %s...", l.Devices[id].Addr.String())
	cln, err := ble.Dial(ctx, l.Devices[id].Addr)
	if err != nil {
		log.Printf("can't Dial %s : %s", l.Devices[id].Addr.String(), err)
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
		log.Printf("can't discover profile of %s: %s", l.Devices[id].Addr.String(), err)
		return
	}
	// Start the exploration.
	explore(cln, p, l.Devices[id])

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

func distance(rssi float64) float64 {

	txPower := float64(-59) //hard coded power value. Usually ranges between -59 to -65

	if rssi == 0 {
		return -1.0
	}

	var ratio = rssi * 1.0 / txPower
	if ratio < 1.0 {
		return math.Pow(ratio, 10)
	}
	var distance = (0.89976)*math.Pow(ratio, 7.7095) + 0.111
	return distance
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
