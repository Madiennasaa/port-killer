package main

import (
	"errors"
	"net"
	"os/exec"
	"strings"
	"syscall"
)

// startDetached is a seam: it launches bin in its own session without waiting,
// since xdg-open may block until the browser exits on some desktops.
var startDetached = func(bin string, args []string) error {
	c := exec.Command(bin, args...)
	c.Env = hardenedEnv()
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		return err
	}
	// Reap the child; its exit status is irrelevant once the URL is handed off.
	go func() { _ = c.Wait() }()
	return nil
}

// portURL (pure) is the http URL for a listener. Wildcard binds map to
// localhost, a specific address is kept (bracketed for IPv6).
func portURL(it PortItem) string {
	host := strings.Trim(it.Addr, "[]")
	if i := strings.Index(host, "%"); i != -1 {
		host = host[:i] // drop an IPv6 zone like %lo
	}
	switch host {
	case "", "*", "0.0.0.0", "::":
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, it.Port) + "/"
}

// openInBrowser opens the listener's URL as the desktop user, so a sudo
// session does not start a browser as root.
func openInBrowser(it PortItem) (string, error) {
	if !strings.HasPrefix(it.Proto, "tcp") {
		return "", errors.New("only TCP listeners can be opened in a browser")
	}
	url := portURL(it)
	bin, argv, err := asUserArgv(desktopUser(), xdgOpenBin, url)
	if err != nil {
		return "", err
	}
	return url, startDetached(bin, argv)
}
