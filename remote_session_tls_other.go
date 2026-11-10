//go:build !linux

package main

import "fmt"

func readHostTLSFile(path string, private bool) ([]byte, error) {
	envName := "POLYPOD_REMOTE_SESSION_TLS_CERT"
	if private {
		envName = "POLYPOD_REMOTE_SESSION_TLS_KEY"
	}
	return nil, fmt.Errorf("%s exige Linux para checar dono e permissão", envName)
}

func readRemoteSessionCAFile(path string) ([]byte, error) {
	return nil, fmt.Errorf("POLYPOD_REMOTE_SESSION_TLS_CA exige Linux para checar permissão")
}
