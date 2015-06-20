package main

import "fmt"
import "log"
import "flag"
import "io/ioutil"
import "os"
import "os/exec"
import "os/signal"
import "runtime"
import "time"
import "bufio"
import "regexp"
import "net"
import "math/rand"
import "sync"

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
	stopped    bool
	processes  map[int]*exec.Cmd
	waitGroup  *sync.WaitGroup
}

func (m *Metasocks) CreateTorConfig(path string, socksAddr string, dataDir string) error {
	config := ""
	config += fmt.Sprintf("SOCKSPort %s\n", socksAddr)
	config += fmt.Sprintf("DataDirectory %s\n", dataDir)
	config += fmt.Sprintf("HardwareAccel 1\n")
	config += fmt.Sprintf("AvoidDiskWrites 0\n")
	return ioutil.WriteFile(path, []byte(config), 0644)
}

func (m *Metasocks) Run(serverAddr, tor, torData, torAddr string, torPortBegin int, num int) error {
	var err error

	m.tor = tor
	m.num = num
	m.serverAddr = serverAddr
	m.waitGroup = &sync.WaitGroup{}

	log.Printf("cleaning dir: %s", torData)
	err = os.RemoveAll(torData)
	if err != nil {
		log.Printf("warning: cannot remove tor data dir %s: %s", torData, err)
	}

	log.Printf("mkdir: %s", torData)
	err = os.MkdirAll(torData, 0755)
	if err != nil {
		return fmt.Errorf("tor data dir mkdir err: %s", err)
	}

	go m.serverRun()
	m.processes = make(map[int]*exec.Cmd)
	for i := 0; i < num; i++ {
		time.Sleep(time.Millisecond * 25)
		addr := fmt.Sprintf("%s:%d", torAddr, torPortBegin+i)
		confPath := fmt.Sprintf("%s/tor_%d.conf", torData, i)
		m.instances = append(m.instances, addr)
		dir := fmt.Sprintf("%s/%d", torData, i)
		if err := m.CreateTorConfig(confPath, addr, dir); err != nil {
			m.stopProcesses()
			return err
		}
		m.waitGroup.Add(1)
		go m.runTor(i, tor, addr, confPath, m.waitGroup)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	s := <-c
	log.Printf("exit, got signal: %s", s)
	m.stopProcesses()
	m.waitGroup.Wait()
	return nil
}

func (m *Metasocks) runTor(torNum int, tor, addr, confPath string, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()
	var err error
	for !m.stopped {
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
		m.processes[torNum] = cmd
		err = cmd.Wait()
		if err != nil {
			log.Printf("tor instance %d wait err: %s", torNum, err)	
		} else {
			log.Printf("tor instance %d stopped", torNum)
		}
	}
}

func (m *Metasocks) stopProcesses() {
	log.Printf("stop all tor instances")
	m.stopped = true
	for num, process := range m.processes {
		log.Printf("kill tor #%d (pid %d)", num, process.Process.Pid)
		err := process.Process.Kill()
		if err != nil {
			log.Printf("tor instance %d kill err: %s", num, err)
		}
	}
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

	if num == 0 {
		log.Fatal("err: num must be great than zero")
	}
	metasocks.Run(serverAddr, tor, torData, torAddr, torPortBegin, num)
}
