package main

import "testing"

func TestParseDaemonArgs(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantSocket string
		wantDebug  bool
	}{
		{
			name:       "defaults when no args",
			args:       []string{},
			wantSocket: "./tmp/ohm.sock",
			wantDebug:  false,
		},
		{
			name:       "socket only",
			args:       []string{"/tmp/ohm.sock"},
			wantSocket: "/tmp/ohm.sock",
			wantDebug:  false,
		},
		{
			name:       "debug flag only",
			args:       []string{"--debug"},
			wantSocket: "./tmp/ohm.sock",
			wantDebug:  true,
		},
		{
			name:       "debug before socket",
			args:       []string{"--debug", "/tmp/ohm.sock"},
			wantSocket: "/tmp/ohm.sock",
			wantDebug:  true,
		},
		{
			name:       "socket before debug",
			args:       []string{"/tmp/ohm.sock", "--debug"},
			wantSocket: "/tmp/ohm.sock",
			wantDebug:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseDaemonArgs(tt.args)
			if got.socketPath != tt.wantSocket {
				t.Errorf("socketPath = %q, want %q", got.socketPath, tt.wantSocket)
			}
			if got.debug != tt.wantDebug {
				t.Errorf("debug = %v, want %v", got.debug, tt.wantDebug)
			}
		})
	}
}
