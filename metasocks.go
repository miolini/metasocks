package main

import "fmt"
import "log"
import "flag"
import "io/ioutil"
import "os"
import "os/exec"
import "runtime"
import "time"
import "bufio"
import "regexp"
import "net"
import "math/rand"

func CheckError(err error) {
    if err != nil {
        log.Fatal(err)
    }
}

type Metasocks struct {
	tor string
	num int
	serverAddr string
	instances []string
}

func (m* Metasocks) CreateTorConfig(path string, socksAddr string, dataDir string) {
	config := ""
	config += fmt.Sprintf("SOCKSPort %s\n", socksAddr)
	config += fmt.Sprintf("DataDirectory %s\n", dataDir)
	config += fmt.Sprintf("HardwareAccel 1\n")
	config += fmt.Sprintf("AvoidDiskWrites 1\n")
	err := ioutil.WriteFile(path, []byte(config), 0644)
	CheckError(err)
}

func (m *Metasocks) Run(serverAddr string, tor string, torData string, num int) {
	m.tor = tor
	m.num = num
	m.serverAddr = serverAddr
	os.Mkdir(torData, 0755)
	for i:=0; i<num; i++ {
		confPath := fmt.Sprintf("%s/tor_%d.conf", torData, i)
		addr := fmt.Sprintf("127.0.0.1:%d", 17001+i)
		m.instances = append(m.instances, addr)
		dir := fmt.Sprintf("%s/%d", torData, i)
		m.CreateTorConfig(confPath, addr, dir)
		
		go func(torNum int) {
			var err error
			for true {
				log.Printf("tor instance %d starting", torNum)
				cmd := exec.Command(tor, "-f", confPath)
				stdout, _ := cmd.StdoutPipe()
				err = cmd.Start()
				if err != nil {
					log.Printf("tor instance %d error: %s", torNum, err)
					time.Sleep(time.Second * 5)
					continue
				}
				scanner := bufio.NewScanner(stdout)
				for scanner.Scan() {
					line := scanner.Text()
					// log.Printf(line)
					if match, _ := regexp.Match("100%", []byte(line)); match {
						log.Printf("tor instance %d started", torNum)
					}
				}
				cmd.Wait()
				log.Printf("tor instance %d stopped", torNum)
			}	
		}(i)
	}
	m.serverRun()
}

func (m *Metasocks) serverRun() {
	log.Printf("run socks5 server on %s", m.serverAddr)
	listener, err := net.Listen("tcp", m.serverAddr)
	if err != nil {
		log.Printf("error listening: %s", err.Error())
		os.Exit(1)
	}
 
	for {
		conn, err := listener.Accept()
		log.Printf("new connection from %s", conn)
		if err != nil {
			log.Printf("Error accept: %s", err.Error())
			return
		}
		go m.clientProcess(conn)
	}
}

func Pipe(connIn net.Conn, connOut net.Conn) {
	buf := make([]byte, 2048)
	log.Printf("start pipe %s to %s", connIn, connOut)
	for n, err := connIn.Read(buf); err != nil; {
		log.Printf("readed %d bytes", n)
		connOut.Write(buf[:n])	
	}
}

func (m *Metasocks) clientProcess(conn net.Conn) {
	addr := m.instances[rand.Int() % len(m.instances)]
	remoteConn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Printf("can't connect to %s", addr)
		conn.Close()
		return
	}
	go Pipe(conn, remoteConn)
	go Pipe(remoteConn, conn)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	var (
		tor	string
		torData string
		num int
		serverAddr string
		metasocks Metasocks
	)

	flag.StringVar(&tor, "tor", "tor", "path to tor executable")
	flag.StringVar(&torData, "tor-data", "data", "path to tor data dirs")
	flag.IntVar(&num, "num", 10, "number of runned tor instances")
	flag.StringVar(&serverAddr, "server", "127.0.0.1:12050", "proxy listen on this host:port")
	flag.Parse()

	log.Printf("tor:  %s", tor)
	log.Printf("num:  %d", num)

	log.Printf("Multisocks starting...")
	metasocks.Run(serverAddr, tor, torData, num)
}