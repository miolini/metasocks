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
import "net/http"
import "golang.org/x/net/proxy"
import "io"

func main() {
	tor := flag.String("tor", "tor", "path to tor executable")
	torData := flag.String("tor-data", "data", "path to tor data dirs")
	num := flag.Int("num", 10, "number of runned tor instances")
	serverAddr := flag.String("server", "127.0.0.1:12050", "proxy listen on this host:port")
	cores := flag.Int("cores", 1, "cpu cores use")
	torAddr := flag.String("tor-addr", "127.0.0.1", "tor listen addr")
	torPortBegin := flag.Int("tor-port-begin", 17001, "tor port begin")
	myipService := flag.String("myip-service", "https://icanhazip.com", "myip webservice url, that return ip as text")
	flag.Parse()

	log.Printf("tor:        %s", *tor)
	log.Printf("torData:    %s", *torData)
	log.Printf("num:        %d", *num)
	log.Printf("serverAddr: %s", *serverAddr)
	log.Printf("cores:      %d", *cores)
	log.Printf("myip service: %s", *myipService)

	log.Printf("Metasocks starting...")

	runtime.GOMAXPROCS(*cores)

	if *num == 0 {
		log.Fatal("err: num must be great than zero")
	}
	var metasocks Metasocks
	metasocks.Run(*serverAddr, *tor, *torData, *torAddr, *torPortBegin, *num, *myipService)
}

func CheckError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

type TorProcess struct {
	Num        int
	ListenAddr string
	Cmd        *exec.Cmd
	ExternalIp string
}

type Metasocks struct {
	tor            string
	num            int
	serverAddr     string
	instances      []string
	stopped        bool
	processes      map[int]*TorProcess
	waitGroup      *sync.WaitGroup
	myipService    string
	processesMutex sync.Mutex
}

func (m *Metasocks) CreateTorConfig(path string, socksAddr string, dataDir string) error {
	config := ""
	config += fmt.Sprintf("SOCKSPort %s\n", socksAddr)
	config += fmt.Sprintf("DataDirectory %s\n", dataDir)
	config += fmt.Sprintf("HardwareAccel 1\n")
	config += fmt.Sprintf("AvoidDiskWrites 0\n")
	return ioutil.WriteFile(path, []byte(config), 0644)
}

func (m *Metasocks) Run(serverAddr, tor, torData, torAddr string,
	torPortBegin int, num int, myipService string) error {
	var err error

	m.tor = tor
	m.num = num
	m.serverAddr = serverAddr
	m.myipService = myipService
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
	m.processes = make(map[int]*TorProcess)
	for i := 0; i < num; i++ {
		time.Sleep(time.Millisecond * 25)
		m.waitGroup.Add(1)
		go func(torNum int) {
			defer m.waitGroup.Done()
			for !m.stopped {
				err := m.runTor(torNum, tor, torData, torAddr, torPortBegin+torNum)
				if err != nil {
					log.Printf("run tor err: %s", err)
				}
				time.Sleep(time.Second)
			}
		}(i)
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, os.Kill)
	s := <-c
	log.Printf("exit, got signal: %s", s)
	m.stopProcesses()
	m.waitGroup.Wait()
	os.RemoveAll(torData)
	return nil
}

func (m *Metasocks) checkExternalIP(torProcess *TorProcess) error {
	socks5Proxy, err := proxy.SOCKS5("tcp", torProcess.ListenAddr, nil, proxy.Direct)
	if err != nil {
		return fmt.Errorf("create socks5 conn for tor %d err: %s", torProcess.Num, err)
	}
	httpClient := &http.Client{Transport: &http.Transport{Dial: socks5Proxy.Dial}}
	res, err := httpClient.Get(m.myipService)
	if err != nil {
		return fmt.Errorf("get external ip for tor %d err: %s", torProcess.Num, err)
	}
	defer res.Body.Close()
	data, err := ioutil.ReadAll(&io.LimitedReader{res.Body, 20})
	if err != nil {
		return fmt.Errorf("read response with external ip for tor %d err: %s", torProcess.Num, err)
	}
	ip := string(data)
	m.processesMutex.Lock()
	for _, proc := range m.processes {
		if proc != nil && proc.ExternalIp == ip {
			err = fmt.Errorf("duplicate external ip for tor %d: %s", torProcess.Num, ip)
			break
		}
	}
	if err == nil {
		torProcess.ExternalIp = ip
	}
	m.processesMutex.Unlock()
	if err != nil {
		return err
	}
	log.Printf("ip for num %d: %s", torProcess.Num, ip)
	return nil
}

func (m *Metasocks) runTor(torNum int, tor, dataDir string, torAddr string, torPort int) error {
	var err error

	addr := fmt.Sprintf("%s:%d", torAddr, torPort)
	confPath := fmt.Sprintf("%s/tor_%d.conf", dataDir, torNum)
	m.instances = append(m.instances, addr)
	dir := fmt.Sprintf("%s/%d", dataDir, torNum)
	if err = os.RemoveAll(dir); err != nil {
		return err
	}
	if err = m.CreateTorConfig(confPath, addr, dir); err != nil {
		return err
	}
	defer func() {
		os.RemoveAll(confPath)
		os.RemoveAll(dir)

	}()

	for !m.stopped {
		log.Printf("tor instance %d starting...", torNum)

		cmd := exec.Command(tor, "-f", confPath)
		torProcess := &TorProcess{Num: torNum, ListenAddr: addr, Cmd: cmd}
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
				break
			}
		}

		err = m.checkExternalIP(torProcess)
		if err != nil {
			if err = cmd.Process.Kill(); err != nil {
				log.Printf("tor instance %d kill err: %s", torNum, err)
			}

		}
		m.processesMutex.Lock()
		m.processes[torNum] = torProcess
		m.processesMutex.Unlock()
		err = cmd.Wait()
		if err != nil {
			log.Printf("tor instance %d wait err: %s", torNum, err)
		} else {
			log.Printf("tor instance %d stopped", torNum)
		}
		m.processesMutex.Lock()
		m.processes[torNum] = nil
		m.processesMutex.Unlock()
	}
	return nil
}

func (m *Metasocks) stopProcesses() {
	log.Printf("stop all tor instances")
	m.stopped = true
	for num, process := range m.processes {
		if process == nil {
			continue
		}
		log.Printf("kill tor #%d (pid %d)", num, process.Cmd.Process.Pid)
		err := process.Cmd.Process.Kill()
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
