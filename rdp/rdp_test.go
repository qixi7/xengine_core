package rdpkit

import (
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"
)

func TestRdp(t *testing.T) {
	printFunc := func(format string, v ...interface{}) {
		format += "\n"
		fmt.Printf(format, v...)
	}
	SetInfoLogger(printFunc)
	//SetDebugLogger(printFunc)
	SetErrorLogger(printFunc)
	listenAddr := "127.0.0.1:19000"
	go server(listenAddr)
	go runClient(listenAddr)
	time.Sleep(10 * time.Second)
	fmt.Printf("finish!\n")
}

func server(addr string) {
	l, err := Listen(addr)
	if err != nil {
		fmt.Printf("[Server err] listen error on %s, because: %v\n", addr, err)
		os.Exit(-1)
		return
	}
	fmt.Printf("[Server] listen at [udp] %s\n", addr)
	// init closeSig
	closeSig := make(chan os.Signal, 1)
	signal.Notify(closeSig, syscall.SIGINT, syscall.SIGTERM)
	// init accept
	go func() {
		for {
			conn, err := l.Accept()
			if err != nil {
				select {
				case _, ok := <-closeSig:
					if !ok {
						fmt.Printf("[Server] Close\n")
						return
					}
				default:
				}
				fmt.Printf("[Server] accept error on: %s, error: %s\n", addr, err)
				return
			}
			go handshakeRDP(conn)
		}
	}()
}

func handshakeRDP(conn net.Conn) {
	fmt.Printf("[Server] recv handshakeRDP!\n")
	conn.(*RdpConn).SetMaxSplitSize(3)
	// reader
	recvBuf := make([]byte, 1200)
	for {
		// 收到啥回复啥
		n, err := conn.Read(recvBuf)
		if err != nil {
			fmt.Printf("[Server] Read err=%v\n", err)
			return
		}
		_, _ = conn.Write(recvBuf[:n])
		fmt.Printf("[Server] Write back n=%d\n", n)
	}
}

func runClient(svrAddr string) {
	// dial
	conn, err := Dial("udp", svrAddr)
	if err != nil {
		fmt.Printf("connect rdp failed, addr=%s, err=%v\n", svrAddr, err)
		return
	}
	conn.(*RdpConn).SetMaxSplitSize(3)
	fmt.Printf("[client] rdp connect to %s success!\n", svrAddr)
	recvBuf := make([]byte, 1200)
	count := 0
	for {
		count++
		// write
		data := []byte(fmt.Sprintf("hello world %d!", count))
		fmt.Printf("[client] write n=%d to server! data=%v\n", len(data), data)
		n, err := conn.Write(data)
		if err != nil {
			fmt.Printf("[client err] rdp Write err=%v!\n", err)
			return
		}
		// read
		n, err = conn.Read(recvBuf)
		if err != nil {
			fmt.Printf("[client err] rdp Read err=%v!\n", err)
			return
		}
		fmt.Printf("[client] read n=%d, content:%s!\n", n, recvBuf[:n])
		time.Sleep(1 * time.Second)
	}
}
