package gin

import (
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestEngineDefaultTimeouts(t *testing.T) {
	router := New()
	assert.Equal(t, 30*time.Second, router.ReadTimeout)
	assert.Equal(t, 10*time.Second, router.ReadHeaderTimeout)
	assert.Equal(t, 30*time.Second, router.WriteTimeout)
	assert.Equal(t, 120*time.Second, router.IdleTimeout)

	routerDefault := Default()
	assert.Equal(t, 30*time.Second, routerDefault.ReadTimeout)
	assert.Equal(t, 10*time.Second, routerDefault.ReadHeaderTimeout)
	assert.Equal(t, 30*time.Second, routerDefault.WriteTimeout)
	assert.Equal(t, 120*time.Second, routerDefault.IdleTimeout)
}

func TestEngineCustomTimeouts(t *testing.T) {
	router := New()
	router.ReadHeaderTimeout = 50 * time.Millisecond

	router.GET("/test", func(c *Context) {
		c.String(http.StatusOK, "ok")
	})

	port, err := getFreePort()
	assert.NoError(t, err)
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	go func() {
		_ = router.Run(addr)
	}()

	// Give the server a moment to start
	time.Sleep(50 * time.Millisecond)

	// Now connect to the server and send headers slowly
	conn, err := net.Dial("tcp", addr)
	assert.NoError(t, err)
	defer conn.Close()

	// Send partial request
	_, err = conn.Write([]byte("GET /test HTTP/1.1\r\n"))
	assert.NoError(t, err)

	// Wait longer than ReadHeaderTimeout (50ms)
	time.Sleep(150 * time.Millisecond)

	// Try to write more, it should fail or the connection should be closed
	_, err = conn.Write([]byte("Host: localhost\r\n\r\n"))
	
	// Read from the connection to see if it was closed
	buf := make([]byte, 1024)
	_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	n, err := conn.Read(buf)
	assert.Error(t, err)
	assert.Equal(t, 0, n)
}

func getFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}
