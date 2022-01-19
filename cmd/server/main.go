package main

import (
	"encoding/binary"
	"encoding/gob"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
	"github.com/hashicorp/yamux"
)

type jsonArgs struct {
	Cwd     string
	Args    []string
	Envs    []string
	Session string
}

func main() {
	flag.Parse()

	SockDir := fmt.Sprintf("/var/run/user/%d/sockpty", os.Getuid())
	if _, err := os.Stat(SockDir); errors.Is(err, os.ErrNotExist) {
		err := os.Mkdir(SockDir, os.ModePerm)
		if err != nil {
			log.Println(err)
		}
	}

	SockAddr := fmt.Sprintf("%s/server.sock", SockDir)
	if err := os.RemoveAll(SockAddr); err != nil {
		log.Fatal(err)
	}
	ln, err := net.ListenUnix("unix", &net.UnixAddr{SockAddr, "unix"})
	if err != nil {
		log.Fatal(err)
	}
	defer os.Remove(SockAddr)
	log.Printf("listening on %s", SockAddr)
	for {
		conn, err := ln.AcceptUnix()
		if err != nil {
			log.Printf("[%s] accept error: %v", conn.RemoteAddr().String(), err)
			continue
		}
		go handle(conn)
	}
}

func handle(conn net.Conn) {
	wg := new(sync.WaitGroup)
	wg.Add(2)
	remote := conn.RemoteAddr().String()
	session, err := yamux.Server(conn, nil)
	if err != nil {
		log.Printf("[%s] session error: %v", remote, err)
		return
	}

	infoChannel, err := session.Accept()
	if err != nil {
		// TODO: handle your error here
		log.Printf("Error infoChannel session.Accept() %s", err)
	}
	bufArgs := make([]byte, 5120)
	bufArgsLength, err := infoChannel.Read(bufArgs)
	if err != nil {
		// TODO: handle your error here
		log.Printf("Error infoChannel.Read() %s", err)
	}
	var args jsonArgs

	err = json.Unmarshal([]byte(bufArgs[:bufArgsLength]), &args)
	if err != nil {
		// TODO: handle your error here
		log.Printf("json.Unmarshal error %s", err)
	}

	clientSession := args.Session
	execCommand := args.Args

	done := make(chan struct{})

	if _, err := exec.LookPath(execCommand[0]); err != nil {
		exitCode := exitCodeToBytes(1)
		if _, err := infoChannel.Write(exitCode); err != nil {
			log.Printf("infochannel error")
		}

		log.Printf("[%s] %s error, command not found", clientSession, execCommand)
	} else {

		exitCode := exitCodeToBytes(0)
		if _, err := infoChannel.Write(exitCode); err != nil {
			log.Printf("infochannel error")
		}

	}

	cmd := exec.Command(execCommand[0], execCommand[1:]...)
	cmd.Dir = args.Cwd
	cmd.Env = args.Envs
	cmd.Env = append(cmd.Env, fmt.Sprintf("HOSTNAME=%s", os.Getenv("HOSTNAME")))

	shellPty, err := pty.Start(cmd)
	if err != nil {
		log.Printf("[%s] pty error: %s"+"\n\n", clientSession, err)
		return
	}

	go func() {
		defer wg.Done()
		if err := cmd.Wait(); err != nil {
			log.Printf("[%s] %s error, exit code: %d", clientSession, execCommand, cmd.ProcessState.ExitCode())
		} else {
			log.Printf("[%s] %s success, exit code: %d", clientSession, execCommand, cmd.ProcessState.ExitCode())
		}

		log.Printf("[%s] %s done"+"\n\n", clientSession, execCommand)

		exitCode := exitCodeToBytes(cmd.ProcessState.ExitCode())
		if _, err := infoChannel.Write(exitCode); err != nil {
			log.Fatal(err)
		}
		infoChannel.Close()
	}()

	controlChannel, err := session.Accept()
	if err != nil {
		log.Printf("[%s] control channel accept error: %v", remote, err)
		return
	}
	go func() {
		defer wg.Done()
		r := gob.NewDecoder(controlChannel)
		for {
			var win struct {
				Rows, Cols int
			}
			if err := r.Decode(&win); err != nil {
				break
			}
			if err := Setsize(shellPty, win.Rows, win.Cols); err != nil {
				log.Printf("[%s] setsize error: %v", remote, err)
				break
			}
			if err := syscall.Kill(cmd.Process.Pid, syscall.SIGWINCH); err != nil {
				log.Printf("[%s] sigwinch error: %v", remote, err)
				break
			}
		}
	}()

	dataChannel, err := session.Accept()
	if err != nil {
		log.Printf("[%s] data channel accept error: %v", remote, err)
		return
	}

	log.Printf("[%s] %s start", clientSession, execCommand)

	go func() {
		if _, err := io.Copy(dataChannel, shellPty); err != nil {
			_ = err
		}
		done <- struct{}{}
	}()
	go func() {
		if _, err := io.Copy(shellPty, dataChannel); err != nil {
			_ = err
		}
		done <- struct{}{}
	}()
	log.Printf("[%s] %s running", clientSession, execCommand)

	<-done

	shellPty.Close()
	dataChannel.Close()

	wg.Wait()

	defer controlChannel.Close()
	defer session.Close()
}

type winsize struct {
	ws_row uint16
	ws_col uint16
}

func Setsize(f *os.File, rows, cols int) error {
	ws := winsize{ws_row: uint16(rows), ws_col: uint16(cols)}
	_, _, errno := syscall.Syscall(
		syscall.SYS_IOCTL,
		f.Fd(),
		syscall.TIOCSWINSZ,
		uintptr(unsafe.Pointer(&ws)),
	)
	if errno != 0 {
		return syscall.Errno(errno)
	}
	return nil
}

func exitCodeToBytes(exitCode int) []byte {
	exitCodeBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(exitCodeBytes, uint64(exitCode))
	return exitCodeBytes
}
