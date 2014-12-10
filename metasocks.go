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
	tor        string
	num        int
	serverAddr string
	instances  []string
}

func (m *Metasocks) CreateTorConfig(path string, socksAddr string, dataDir string) {
	config := ""
	config += fmt.Sprintf("SOCKSPort %s\n", socksAddr)
	config += fmt.Sprintf("DataDirectory %s\n", dataDir)
	config += fmt.Sprintf("HardwareAccel 1\n")
	config += fmt.Sprintf("AvoidDiskWrites 0\n")
	err := ioutil.WriteFile(path, []byte(config), 0644)
	CheckError(err)
}

func (m *Metasocks) Run(serverAddr string, tor string, torData string, torAddr string, torPortBegin int, num int) {
	m.tor = tor
	m.num = num
	m.serverAddr = serverAddr
	os.Mkdir(torData, 0755)
	go m.serverRun()
	for i := 0; i < num; i++ {
		time.Sleep(time.Millisecond * 25)
		confPath := fmt.Sprintf("%s/tor_%d.conf", torData, i)
		addr := fmt.Sprintf("%s:%d", torAddr, torPortBegin+i)
		m.instances = append(m.instances, addr)
		dir := fmt.Sprintf("%s/%d", torData, i)
		m.CreateTorConfig(confPath, addr, dir)

		go func(torNum int) {
			var err error
			for true {
				log.Printf("tor instance %d starting...", torNum)
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
					//log.Printf(line)
					if match, _ := regexp.Match("100%", []byte(line)); match {
						log.Printf("tor instance %d started", torNum)
						break
					}
				}
				cmd.Wait()
				log.Printf("tor instance %d stopped", torNum)
			}
		}(i)
	}
	<-make(chan bool)
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
		// log.Printf("new connection from %s", conn)
		if err != nil {
			log.Printf("Error accept: %s", err.Error())
			continue
		}
		go m.clientProcess(conn)
	}
}

func Pipe(connIn net.Conn, connOut net.Conn) {
	var (
		n   int
		err error
	)
	defer connIn.Close()
	defer connOut.Close()
	buf := make([]byte, 2048)
	for true {
		n, err = connIn.Read(buf)
		if err != nil {
			break
		}
		// log.Printf("readed %d bytes", n)
		n, err = connOut.Write(buf[:n])
		if err != nil {
			break
		}
	}
}

func (m *Metasocks) clientProcess(conn net.Conn) {
	if len(m.instances) == 0 {
		log.Printf("WARNING: close client connection because no one tor instance in pool right now")
		conn.Close()
		return
	}
	addr := m.instances[rand.Int()%len(m.instances)]
	remoteConn, err := net.Dial("tcp4", addr)
	if err != nil {
		log.Printf("can't connect to %s", addr)
		conn.Close()
		return
	}
	go Pipe(conn, remoteConn)
	go Pipe(remoteConn, conn)
}

func main() {
	var (
		tor          string
		torData      string
		num          int
		serverAddr   string
		metasocks    Metasocks
		cores        int
		torAddr      string
		torPortBegin int
	)

	flag.StringVar(&tor, "tor", "tor", "path to tor executable")
	flag.StringVar(&torData, "tor-data", "data", "path to tor data dirs")
	flag.IntVar(&num, "num", 10, "number of runned tor instances")
	flag.StringVar(&serverAddr, "server", "127.0.0.1:12050", "proxy listen on this host:port")
	flag.IntVar(&cores, "cores", 1, "cpu cores use")
	flag.StringVar(&torAddr, "tor-addr", "127.0.0.1", "tor listen addr")
	flag.IntVar(&torPortBegin, "tor-port-begin", 17001, "tor port begin")
	flag.Parse()

	log.Printf("tor:        %s", tor)
	log.Printf("torData:    %s", torData)
	log.Printf("num:        %d", num)
	log.Printf("serverAddr: %s", serverAddr)
	log.Printf("cores:      %d", cores)

	log.Printf("Metasocks starting...")

	runtime.GOMAXPROCS(cores)
	metasocks.Run(serverAddr, tor, torData, torAddr, torPortBegin, num)
}
