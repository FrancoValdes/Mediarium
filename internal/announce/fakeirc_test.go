package announce

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeIRC is a tiny in-memory IRC server: just enough of the protocol to test
// a client's registration, SASL, NickServ, invites, joins and channel
// messages, with a way to say things as any nick and to cut every connection.
type fakeIRC struct {
	t  *testing.T
	ln net.Listener

	// Behaviour, set before the first connection.
	saslUser, saslPass string // when set, SASL PLAIN is offered and these are the right login
	nickservPass       string // when set, "NickServ IDENTIFY <this>" is the right sign-in
	inviteOnly         map[string]bool
	inviteBot          string // a private message to this nick invites the sender to inviteChan
	inviteText         string
	inviteChan         string
	bigLine            bool // sends an over-long line right after the welcome

	mu       sync.Mutex
	conns    []*fakeConn
	received []string // every line any client sent, "nick> line"
	invited  map[string]bool
	welcomes int
}

type fakeConn struct {
	srv      *fakeIRC
	c        net.Conn
	wmu      sync.Mutex
	nick     string
	gotUser  bool
	capReq   bool
	capEnd   bool
	saslOK   bool
	welcomed bool
	channels map[string]bool
}

func newFakeIRC(t *testing.T) *fakeIRC {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIRC{t: t, ln: ln, inviteOnly: map[string]bool{}, invited: map[string]bool{}}
	t.Cleanup(f.Close)
	return f
}

// serve starts accepting. cert, when set, makes the server speak TLS.
func (f *fakeIRC) serve(cert *tls.Certificate) {
	ln := f.ln
	if cert != nil {
		ln = tls.NewListener(f.ln, &tls.Config{Certificates: []tls.Certificate{*cert}})
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			fc := &fakeConn{srv: f, c: c, channels: map[string]bool{}}
			f.mu.Lock()
			f.conns = append(f.conns, fc)
			f.mu.Unlock()
			go fc.loop()
		}
	}()
}

func (f *fakeIRC) addr() (string, int) {
	a := f.ln.Addr().(*net.TCPAddr)
	return a.IP.String(), a.Port
}

func (f *fakeIRC) Close() {
	f.ln.Close()
	f.DropAll()
}

// DropAll cuts every client connection.
func (f *fakeIRC) DropAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.conns {
		c.c.Close()
	}
	f.conns = nil
}

// Say sends a channel message from any nick to every client that is in the channel.
func (f *fakeIRC) Say(from, target, text string) { f.send("PRIVMSG", from, target, text) }

// Notice is Say with NOTICE.
func (f *fakeIRC) Notice(from, target, text string) { f.send("NOTICE", from, target, text) }

func (f *fakeIRC) send(cmd, from, target, text string) {
	f.mu.Lock()
	conns := append([]*fakeConn(nil), f.conns...)
	f.mu.Unlock()
	for _, c := range conns {
		c.wmu.Lock()
		in := c.channels[strings.ToLower(target)]
		c.wmu.Unlock()
		if in || !strings.HasPrefix(target, "#") {
			c.write(fmt.Sprintf(":%s!u@h %s %s :%s", from, cmd, target, text))
		}
	}
}

func (f *fakeIRC) lines() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.received...)
}

// saw reports whether any client sent a line containing sub.
func (f *fakeIRC) saw(sub string) bool {
	for _, l := range f.lines() {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func (f *fakeIRC) welcomeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.welcomes
}

func (c *fakeConn) write(line string) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, _ = c.c.Write([]byte(line + "\r\n"))
}

func (c *fakeConn) joined(ch string) bool {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.channels[strings.ToLower(ch)]
}

func (c *fakeConn) loop() {
	defer c.c.Close()
	r := bufio.NewReader(c.c)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		c.srv.mu.Lock()
		c.srv.received = append(c.srv.received, c.nick+"> "+line)
		c.srv.mu.Unlock()
		c.handle(line)
	}
}

func (c *fakeConn) maybeWelcome() {
	f := c.srv
	if c.welcomed || c.nick == "" || !c.gotUser {
		return
	}
	if f.saslUser != "" && c.capReq && !c.capEnd {
		return
	}
	c.welcomed = true
	f.mu.Lock()
	f.welcomes++
	f.mu.Unlock()
	c.write(":irc.test 001 " + c.nick + " :Welcome")
	c.write(":irc.test 376 " + c.nick + " :End of MOTD")
	if f.bigLine {
		c.write(":irc.test NOTICE " + c.nick + " :" + strings.Repeat("x", 9000))
	}
}

func (c *fakeConn) handle(line string) {
	f := c.srv
	cmd, rest, _ := strings.Cut(line, " ")
	switch strings.ToUpper(cmd) {
	case "CAP":
		switch {
		case strings.HasPrefix(rest, "REQ"):
			c.capReq = true
			if f.saslUser != "" {
				c.write(":irc.test CAP * ACK :sasl")
			} else {
				c.write(":irc.test CAP * NAK :sasl")
			}
		case strings.HasPrefix(rest, "END"):
			c.capEnd = true
			c.maybeWelcome()
		}
	case "NICK":
		c.nick = strings.TrimSpace(rest)
		c.maybeWelcome()
	case "USER":
		c.gotUser = true
		c.maybeWelcome()
	case "AUTHENTICATE":
		if rest == "PLAIN" {
			c.write("AUTHENTICATE +")
			return
		}
		raw, _ := base64.StdEncoding.DecodeString(rest)
		parts := strings.Split(string(raw), "\x00")
		if len(parts) == 3 && parts[1] == f.saslUser && parts[2] == f.saslPass {
			c.saslOK = true
			c.write(":irc.test 900 " + c.nick + " " + c.nick + "!u@h " + parts[1] + " :You are now logged in")
			c.write(":irc.test 903 " + c.nick + " :SASL authentication successful")
		} else {
			c.write(":irc.test 904 " + c.nick + " :SASL authentication failed")
		}
	case "PRIVMSG":
		target, text, _ := strings.Cut(rest, " :")
		switch {
		case strings.EqualFold(target, "NickServ"):
			if strings.TrimPrefix(text, "IDENTIFY ") == f.nickservPass {
				c.write(":NickServ!NickServ@services NOTICE " + c.nick + " :You are now identified for " + c.nick + ".")
			} else {
				c.write(":NickServ!NickServ@services NOTICE " + c.nick + " :Password incorrect.")
			}
		case f.inviteBot != "" && strings.EqualFold(target, f.inviteBot) && text == f.inviteText:
			f.mu.Lock()
			f.invited[strings.ToLower(c.nick)] = true
			f.mu.Unlock()
			c.write(":" + f.inviteBot + "!u@h INVITE " + c.nick + " :" + f.inviteChan)
		}
	case "JOIN":
		ch, _, _ := strings.Cut(rest, " ")
		f.mu.Lock()
		blocked := f.inviteOnly[strings.ToLower(ch)] && !f.invited[strings.ToLower(c.nick)]
		f.mu.Unlock()
		if blocked {
			c.write(":irc.test 473 " + c.nick + " " + ch + " :Cannot join channel (+i)")
			return
		}
		c.wmu.Lock()
		c.channels[strings.ToLower(ch)] = true
		c.wmu.Unlock()
		c.write(":" + c.nick + "!u@h JOIN " + ch)
		c.write(":irc.test 366 " + c.nick + " " + ch + " :End of /NAMES list.")
	case "PING":
		c.write(":irc.test PONG irc.test :" + strings.TrimPrefix(rest, ":"))
	case "QUIT":
		c.c.Close()
	}
}

// selfSigned makes a certificate for 127.0.0.1 that no client trusts.
func selfSigned(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "irc.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"irc.test"},
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// waitFor polls until cond is true or the time is up.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
