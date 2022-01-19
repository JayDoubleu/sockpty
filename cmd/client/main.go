package main

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/hashicorp/yamux"
	"golang.org/x/term"
)

type spawnCommandArgs struct {
	Cwd     string
	Args    []string
	Envs    []string
	Session string
}

func main() {
	flag.Parse()
	if len(os.Args[1:]) == 0 {
		log.Fatal("ERROR: Please specify command to execute...")
	}

	termRaw, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		log.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}

	err = term.Restore(int(os.Stdin.Fd()), termRaw)
	if err != nil {
		log.Fatal(err)
	}

	clientSessionName, clientSockAddr, serverSockAddr := SockPaths("client-", ".sock")

	envs := os.Environ()
	envRemove(envs, "container")
	envRemove(envs, "TOOLBOX_PATH")
	envRemove(envs, "DISTTAG")
	envRemove(envs, "FGC")

	args := spawnCommandArgs{
		Cwd:     wd,
		Args:    os.Args[1:],
		Envs:    envs,
		Session: clientSessionName,
	}

	var spawnCommand []byte
	spawnCommand, err = json.Marshal(args)
	if err != nil {
		log.Println(err)
	}

	connType := "unix"
	laddr := net.UnixAddr{clientSockAddr, connType}
	conn, err := net.DialUnix(connType, &laddr,
		&net.UnixAddr{serverSockAddr, connType})
	if err != nil {
		log.Fatal("Unable to establish connection with the server")
	}

	session, err := yamux.Client(conn, nil)
	if err != nil {
		log.Fatalf("session error: %v", err)
	}

	stdin := int(os.Stdin.Fd())
	if !term.IsTerminal(stdin) {
		log.Fatal("not on a terminal")
	}

	oldState, err := term.MakeRaw(stdin)
	if err != nil {
		log.Fatalf("unable to make term.raw: %v", err)
	}

	infoChannel, err := session.Open()
	if err != nil {
		log.Fatalf("info channel open error: %v", err)
	}

	_, err = infoChannel.Write(spawnCommand)
	if err != nil {
		log.Printf("info channel write error %v", err)
	}

	defer func() { _ = term.Restore(int(os.Stdin.Fd()), oldState) }()

	done := make(chan struct{})
	exitDone := make(chan struct{})

	lookPath := make([]byte, 8)
	_, err = infoChannel.Read(lookPath)
	if err != nil {
		log.Printf("lookup bin path error %v", err)
	}

	exitCode := bytesToExitCode(lookPath)
	if exitCode == 1 {
		session.Close()
		os.Remove(clientSockAddr)
		err = term.Restore(int(os.Stdin.Fd()), termRaw)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("%s: command not found", args.Args[0])
		os.Exit(1)
	} else {

		controlChannel, err := session.Open()
		if err != nil {
			log.Fatalf("control channel open error: %v", err)
		}
		go func() {
			w := gob.NewEncoder(controlChannel)
			c := make(chan os.Signal, 1)
			signal.Notify(c, syscall.SIGWINCH)
			for {
				//cols, rows := 75, 428
				cols, rows, err := term.GetSize(stdin)
				if err != nil {
					log.Printf("getsize error: %v", err)
					break
				}
				win := struct {
					Rows, Cols int
				}{Rows: rows, Cols: cols}
				if err := w.Encode(win); err != nil {
					break
				}
				<-c
			}
			done <- struct{}{}
		}()

		dataChannel, err := session.Open()
		if err != nil {
			log.Fatalf("data channel open error: %v", err)
		}

		go func() {
			if _, err := io.Copy(dataChannel, os.Stdin); err != nil {
				log.Fatal(err)
			}
			done <- struct{}{}

		}()
		go func() {
			if _, err := io.Copy(os.Stdout, dataChannel); err != nil {
				log.Fatal(err)
			}
			done <- struct{}{}
		}()
		<-done

		go func() {
			exitCodeBytes := make([]byte, 8)
			_, err := infoChannel.Read(exitCodeBytes)
			if err != nil {
				log.Printf("info channel get exit code error: %v", err)
			}
			exitCode = bytesToExitCode(exitCodeBytes)
			infoChannel.Close()
			exitDone <- struct{}{}
		}()
		<-exitDone

		session.Close()
		err = term.Restore(int(os.Stdin.Fd()), termRaw)
		if err != nil {
			log.Fatal(err)
		}
		os.Remove(clientSockAddr)

		defer func() { os.Exit(exitCode) }()
	}

}

func SockPaths(prefix, suffix string) (string, string, string) {
	randBytes := make([]byte, 6)
	if _, err := rand.Read(randBytes); err != nil {
		log.Fatal(err)
	}

	sockDir := fmt.Sprintf("/var/run/user/%d/sockpty", os.Getuid())
	clientSessionName := hex.EncodeToString(randBytes)
	clientSockAddr := filepath.Join(sockDir, prefix+clientSessionName+suffix)
	serverSockAddr := sockDir + "/server.sock"
	return string(clientSessionName), clientSockAddr, serverSockAddr
}

func envRemove(s []string, r string) []string {
	for i, v := range s {
		if strings.HasPrefix(v, r+"=") {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}

func bytesToExitCode(exitCodeBytes []byte) int {
	exitCode := binary.LittleEndian.Uint64(exitCodeBytes)
	return int(exitCode)
}
