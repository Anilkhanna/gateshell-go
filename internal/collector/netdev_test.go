package collector

import "testing"

const sampleProcNetDev = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  129600     1600    0    0    0     0          0         0   129600     1600    0    0    0     0       0          0
  eth0: 123456789  654321    0    0    0     0          0         0 987654321  456789    0    0    0     0       0          0
`

func TestParseNetDev(t *testing.T) {
	iface, ok := parseNetDev(sampleProcNetDev)
	if !ok {
		t.Fatal("expected an interface to be found")
	}
	if iface.name != "eth0" {
		t.Errorf("name = %q, want eth0", iface.name)
	}
	if iface.rxBytes != 123456789 {
		t.Errorf("rxBytes = %d, want 123456789", iface.rxBytes)
	}
	if iface.txBytes != 987654321 {
		t.Errorf("txBytes = %d, want 987654321", iface.txBytes)
	}
}

func TestParseNetDev_SkipsLoopbackVariants(t *testing.T) {
	const data = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  129600     1600    0    0    0     0          0         0   129600     1600    0    0    0     0       0          0
loop0:  129600     1600    0    0    0     0          0         0   129600     1600    0    0    0     0       0          0
  wlan0: 111 222    0    0    0     0          0         0 333 444    0    0    0     0       0          0
`
	iface, ok := parseNetDev(data)
	if !ok {
		t.Fatal("expected wlan0 to be found")
	}
	if iface.name != "wlan0" {
		t.Errorf("name = %q, want wlan0", iface.name)
	}
}

func TestParseNetDev_OnlyLoopback(t *testing.T) {
	const data = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo:  129600     1600    0    0    0     0          0         0   129600     1600    0    0    0     0       0          0
`
	if _, ok := parseNetDev(data); ok {
		t.Fatal("expected no interface when only loopback is present")
	}
}

func TestParseNetDev_EmptyInput(t *testing.T) {
	if _, ok := parseNetDev(""); ok {
		t.Fatal("expected ok=false for empty input")
	}
}

func TestParseNetDev_MalformedCounters(t *testing.T) {
	const data = `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
  eth0: not-a-number  654321    0    0    0     0          0         0 987654321  456789    0    0    0     0       0          0
`
	if _, ok := parseNetDev(data); ok {
		t.Fatal("expected ok=false for malformed counters")
	}
}
