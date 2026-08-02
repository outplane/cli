package core

import "testing"

// The two functions below decide what is sent to an endpoint that deletes
// whatever it is not told about, so a mistake in either one silently removes a
// port somebody is serving traffic on. They are pure, so they can be held to
// that here rather than against a live application.

func endpoints(spec ...NewPort) []Endpoint {
	out := make([]Endpoint, 0, len(spec))
	for _, s := range spec {
		out = append(out, Endpoint{Port: s.Port, Scheme: s.Scheme, Public: s.Public})
	}
	return out
}

func equal(t *testing.T, got, want []NewPort) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d ports, want %d: %+v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] got %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestMergePorts(t *testing.T) {
	existing := endpoints(
		NewPort{Port: 3000, Scheme: "http", Public: true},
		NewPort{Port: 5432, Scheme: "tcp", Public: false},
	)

	t.Run("adding one keeps the others", func(t *testing.T) {
		got := MergePorts(existing, []NewPort{{Port: 8080, Scheme: "http"}})
		equal(t, got, []NewPort{
			{Port: 3000, Scheme: "http", Public: true},
			{Port: 5432, Scheme: "tcp"},
			{Port: 8080, Scheme: "http"},
		})
	})

	t.Run("changing one replaces only that one", func(t *testing.T) {
		got := MergePorts(existing, []NewPort{{Port: 5432, Scheme: "tcp", Public: true}})
		equal(t, got, []NewPort{
			{Port: 3000, Scheme: "http", Public: true},
			{Port: 5432, Scheme: "tcp", Public: true},
		})
	})

	t.Run("several at once", func(t *testing.T) {
		got := MergePorts(existing, []NewPort{
			{Port: 8080, Scheme: "h2c"},
			{Port: 3000, Scheme: "http"},
		})
		equal(t, got, []NewPort{
			{Port: 3000, Scheme: "http", Public: false},
			{Port: 5432, Scheme: "tcp"},
			{Port: 8080, Scheme: "h2c"},
		})
	})

	t.Run("nothing incoming changes nothing", func(t *testing.T) {
		got := MergePorts(existing, nil)
		equal(t, got, []NewPort{
			{Port: 3000, Scheme: "http", Public: true},
			{Port: 5432, Scheme: "tcp"},
		})
	})

	t.Run("the first port of an application with none", func(t *testing.T) {
		got := MergePorts(nil, []NewPort{{Port: 3000, Scheme: "http", Public: true}})
		equal(t, got, []NewPort{{Port: 3000, Scheme: "http", Public: true}})
	})
}

func TestWithoutPorts(t *testing.T) {
	existing := endpoints(
		NewPort{Port: 3000, Scheme: "http", Public: true},
		NewPort{Port: 5432, Scheme: "tcp"},
		NewPort{Port: 8080, Scheme: "h2c"},
	)

	t.Run("removing one keeps the others", func(t *testing.T) {
		equal(t, WithoutPorts(existing, []int{5432}), []NewPort{
			{Port: 3000, Scheme: "http", Public: true},
			{Port: 8080, Scheme: "h2c"},
		})
	})

	t.Run("removing several", func(t *testing.T) {
		equal(t, WithoutPorts(existing, []int{3000, 8080}), []NewPort{{Port: 5432, Scheme: "tcp"}})
	})

	t.Run("removing the last one leaves an empty set, not the old one", func(t *testing.T) {
		got := WithoutPorts(existing, []int{3000, 5432, 8080})
		if len(got) != 0 {
			t.Fatalf("got %+v, want nothing", got)
		}
	})

	t.Run("a number that is not there removes nothing", func(t *testing.T) {
		equal(t, WithoutPorts(existing, []int{9999}), []NewPort{
			{Port: 3000, Scheme: "http", Public: true},
			{Port: 5432, Scheme: "tcp"},
			{Port: 8080, Scheme: "h2c"},
		})
	})
}

func TestCheckPort(t *testing.T) {
	valid := []NewPort{
		{Port: 1, Scheme: "http"},
		{Port: 65535, Scheme: "tcp"},
		{Port: 3000, Scheme: "h2c"},
	}
	for _, p := range valid {
		if err := CheckPort(p); err != nil {
			t.Errorf("%+v was refused: %v", p, err)
		}
	}

	invalid := []NewPort{
		{Port: 0, Scheme: "http"},
		{Port: 65536, Scheme: "http"},
		{Port: -1, Scheme: "http"},
		{Port: 3000, Scheme: "https"},
		{Port: 3000, Scheme: ""},
	}
	for _, p := range invalid {
		if err := CheckPort(p); err == nil {
			t.Errorf("%+v was accepted", p)
		}
	}
}
