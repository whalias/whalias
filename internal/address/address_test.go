package address

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestIdentityComposeAndName(t *testing.T) {
	labels := map[string]string{
		labelProject: "demo",
		labelService: "web",
		labelNumber:  "1",
	}
	if got := Identity("/demo-web-1", labels); got != "compose:demo/web/1" {
		t.Fatalf("identity %s", got)
	}
	partial := map[string]string{labelProject: "demo", labelService: "web"}
	if got := Identity("/web", partial); got != "name:web" {
		t.Fatalf("fallback identity %s", got)
	}
	if got := Identity("plain", nil); got != "name:plain" {
		t.Fatalf("plain identity %s", got)
	}
}

func TestDNSNames(t *testing.T) {
	labels := map[string]string{labelProject: "my_app", labelService: "web"}
	names := DNSNames("/wizardly_banzai", labels)
	want := []string{"wizardly-banzai.wha.internal", "web.my-app.wha.internal"}
	if len(names) != len(want) {
		t.Fatalf("names %#v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("name %d = %s, want %s", i, names[i], want[i])
		}
	}
	dotted := DNSNames("web.myproject", nil)
	if len(dotted) != 1 || dotted[0] != "web.myproject.wha.internal" {
		t.Fatalf("dotted %#v", dotted)
	}
	reserved := DNSNames("whalias", nil)
	if len(reserved) != 0 {
		t.Fatalf("reserved name published: %#v", reserved)
	}
	if got := Preferred(names); got != "web.my-app.wha.internal" {
		t.Fatalf("preferred %s", got)
	}
}

func TestAllocateReusePersistAndDNS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addresses.json")
	book, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{labelProject: "demo", labelService: "web", labelNumber: "1"}
	names := DNSNames("demo-web-1", labels)
	ip, err := book.Upsert(Identity("demo-web-1", labels), names)
	if err != nil {
		t.Fatal(err)
	}
	if ip.String() != "10.254.253.1" {
		t.Fatalf("first address %s", ip)
	}
	if _, ipnet, err := net.ParseCIDR(Subnet); err != nil || !ipnet.Contains(ip) {
		t.Fatalf("address outside %s", Subnet)
	}
	again, err := book.Upsert(Identity("demo-web-1", labels), names)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Equal(ip) {
		t.Fatalf("recreate changed address to %s", again)
	}
	otherLabels := map[string]string{labelProject: "demo", labelService: "web", labelNumber: "2"}
	otherNames := DNSNames("demo-web-2", otherLabels)
	ip2, err := book.Upsert(Identity("demo-web-2", otherLabels), otherNames)
	if err != nil {
		t.Fatal(err)
	}
	if ip2.String() != "10.254.253.2" {
		t.Fatalf("second address %s", ip2)
	}
	if err := book.SetRuntime(Identity("demo-web-1", labels), "", "172.18.0.3", 80); err != nil {
		t.Fatal(err)
	}
	got, ok := book.Records("Web.Demo.wha.internal.")
	if !ok || len(got) != 2 {
		t.Fatalf("service records %#v ok=%v", got, ok)
	}
	if got[0].String() != "10.254.253.1" || got[1].String() != "10.254.253.2" {
		t.Fatalf("records %v", got)
	}
	for _, rec := range got {
		if rec.String() == "172.18.0.3" {
			t.Fatal("DNS published the bridge address")
		}
	}
	one, ok := book.Records("demo-web-1.wha.internal")
	if !ok || len(one) != 1 || one[0].String() != "10.254.253.1" {
		t.Fatalf("container record %#v", one)
	}
	name, bridge, port, ok := book.Lookup(ip)
	if !ok || name != "web.demo.wha.internal" || bridge != "172.18.0.3" || port != 80 {
		t.Fatalf("lookup %s %s %d %v", name, bridge, port, ok)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("address file mode %o", info.Mode().Perm())
	}

	reloaded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	kept, ok := reloaded.Records("web.demo.wha.internal")
	if !ok || len(kept) != 2 {
		t.Fatal("reload dropped DNS records")
	}
	_, bridge, _, ok = reloaded.Lookup(net.ParseIP("10.254.253.1"))
	if !ok || bridge != "172.18.0.3" {
		t.Fatalf("reload lookup bridge %s ok=%v", bridge, ok)
	}
	if _, ok := reloaded.Records("missing.wha.internal"); ok {
		t.Fatal("unknown name returned a record")
	}
}

func TestOpenRejectsAddressesOutsideStableSubnet(t *testing.T) {
	cases := map[string]string{
		"bridge":    `{"allocations":[{"identity":"name:web","ip":"172.18.0.3","names":["web.wha.internal"]}]}`,
		"tunnel":    `{"allocations":[{"identity":"name:web","ip":"10.254.254.2","names":["web.wha.internal"]}]}`,
		"network":   `{"allocations":[{"identity":"name:web","ip":"10.254.253.0","names":["web.wha.internal"]}]}`,
		"broadcast": `{"allocations":[{"identity":"name:web","ip":"10.254.253.255","names":["web.wha.internal"]}]}`,
		"duplicate ip": `{"allocations":[` +
			`{"identity":"name:a","ip":"10.254.253.1","names":["a.wha.internal"]},` +
			`{"identity":"name:b","ip":"10.254.253.1","names":["b.wha.internal"]}]}`,
		"duplicate identity": `{"allocations":[` +
			`{"identity":"name:a","ip":"10.254.253.1","names":["a.wha.internal"]},` +
			`{"identity":"name:a","ip":"10.254.253.2","names":["a.wha.internal"]}]}`,
	}
	for name, body := range cases {
		path := filepath.Join(t.TempDir(), "addresses.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(path); err == nil {
			t.Fatalf("%s: opened an invalid table", name)
		}
	}
}

func TestOpenDropsInvalidStoredBridge(t *testing.T) {
	path := filepath.Join(t.TempDir(), "addresses.json")
	body := `{"allocations":[{"identity":"name:web","ip":"10.254.253.1","names":["web.wha.internal"],"primary":"web.wha.internal","bridge":"10.254.253.9","httpPort":80}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	book, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := book.Lookup(net.ParseIP("10.254.253.1")); ok {
		t.Fatal("proxy lookup used a bridge inside the stable subnet")
	}
	got, ok := book.Records("web.wha.internal")
	if !ok || len(got) != 1 || got[0].String() != "10.254.253.1" {
		t.Fatalf("records %v ok=%v", got, ok)
	}
}

func TestSetRuntimeRefusesReservedBridgeAndClears(t *testing.T) {
	book, err := Open(filepath.Join(t.TempDir(), "addresses.json"))
	if err != nil {
		t.Fatal(err)
	}
	ip, err := book.Upsert("name:web", []string{"web.wha.internal"})
	if err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []string{"10.254.253.7", "10.254.254.1", "127.0.0.1", "0.0.0.0", "nope"} {
		if err := book.SetRuntime("name:web", "", bridge, 80); err == nil {
			t.Fatalf("accepted bridge %s", bridge)
		}
	}
	if err := book.SetRuntime("name:web", "", "172.18.0.3", 0); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := book.Lookup(ip); ok {
		t.Fatal("proxy lookup succeeded with httpPort 0")
	}
	if err := book.SetRuntime("name:web", "", "172.18.0.3", 80); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := book.Lookup(ip); !ok {
		t.Fatal("lookup missing after runtime set")
	}
	entries := book.Entries()
	if len(entries) != 1 || entries[0].Identity != "name:web" || entries[0].IP != ip.String() || entries[0].Bridge != "172.18.0.3" {
		t.Fatalf("entries %#v", entries)
	}
	if err := book.SetRuntime("name:web", "", "", 80); err != nil {
		t.Fatal(err)
	}
	if _, _, _, ok := book.Lookup(ip); ok {
		t.Fatal("proxy still forwards to a cleared bridge")
	}
	got, ok := book.Records("web.wha.internal")
	if !ok || len(got) != 1 || !got[0].Equal(ip) {
		t.Fatalf("clearing the bridge dropped the DNS record: %v %v", got, ok)
	}
}

func TestDNSNamesClampLongLabels(t *testing.T) {
	long := "a"
	for len(long) < 80 {
		long += "b"
	}
	names := DNSNames(long, nil)
	if len(names) != 1 {
		t.Fatalf("names %#v", names)
	}
	label := names[0][:len(names[0])-len("."+Domain)]
	if len(label) != 63 {
		t.Fatalf("label length %d, want 63", len(label))
	}
}

func TestBlockFull(t *testing.T) {
	book, err := Open(filepath.Join(t.TempDir(), "full.json"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 254; i++ {
		id := "name:" + string(rune('a'+(i%26))) + "-" + itoa(i)
		if _, err := book.Upsert(id, []string{id + ".wha.internal"}); err != nil {
			t.Fatalf("allocate %d: %v", i, err)
		}
	}
	if _, err := book.Upsert("name:overflow", []string{"overflow.wha.internal"}); err == nil {
		t.Fatal("expected a full block")
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [16]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
