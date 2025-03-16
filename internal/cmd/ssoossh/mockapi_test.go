// Created By Mike Nestor <me@mikenestor.org>
package ssoossh

import (
	"errors"

	"github.com/mnestor/ssoossh/internal/ssh"
	"github.com/stretchr/testify/mock"
)

type MockApiClientGood struct {
	mock.Mock
}

func (m *MockApiClientGood) GetCA() (string, error) {
	return "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFoeGjSXgh1UVJ4UwsYeMfB33yrTXDN589O0tT2Cp1UX", nil
}
func (m *MockApiClientGood) GetCertificate(string) (string, error) {
	return "", nil
}
func (m *MockApiClientGood) PostPubKey(ssh.KeyPairI) (string, error) {
	return "", errors.New("")
}

type MockApiClientFail struct {
	mock.Mock
}

func (m *MockApiClientFail) GetCA() (string, error) {
	return "", errors.New("unable to talk to server please check your configuration")
}
func (m *MockApiClientFail) PostPubKey(ssh.KeyPairI) (string, error) {
	return "", errors.New("")
}
func (m *MockApiClientFail) GetCertificate(string) (string, error) {
	return "", errors.New("")
}

type MockApiClientUhh struct {
	mock.Mock
}

func (m *MockApiClientUhh) GetCA() (string, error) {
	return "", nil
}
func (m *MockApiClientUhh) PostPubKey(ssh.KeyPairI) (string, error) {
	return "", errors.New("")
}
func (m *MockApiClientUhh) GetCertificate(string) (string, error) {
	return "", errors.New("")
}
