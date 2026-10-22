package main

import (
	"reflect"
	"testing"
)

func TestServerOverrides(t *testing.T) {
	cases := []struct {
		args []string
		want map[string]string
	}{
		{nil, map[string]string{}},
		{[]string{"/data"}, map[string]string{"BIN_DATA_DIR": "/data"}},
		{[]string{"/data", "--console-address", ":9001"}, map[string]string{"BIN_DATA_DIR": "/data", "BIN_CONSOLE_ADDR": ":9001"}},
		{[]string{"--address=:9100", "/srv/bin", "--console-address=:9101"},
			map[string]string{"BIN_DATA_DIR": "/srv/bin", "BIN_S3_ADDR": ":9100", "BIN_CONSOLE_ADDR": ":9101"}},
	}
	for _, tc := range cases {
		got, err := serverOverrides(tc.args)
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%q: got %v, %v; want %v", tc.args, got, err, tc.want)
		}
	}
	for _, bad := range [][]string{{"--port", "9000"}, {"/a", "/b"}, {"--address"}} {
		if _, err := serverOverrides(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}
