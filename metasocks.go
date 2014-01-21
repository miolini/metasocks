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

func CheckError(err error) {
    if err != nil {
        log.Fatal(err)
    }
}

type Metasocks struct {
	tor string
	num int
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

func (m *Metasocks) Run(tor string, torData string, num int) {
	m.tor = tor
	m.num = num
	os.Mkdir(torData, 0755)
	for i:=0; i<num; i++ {
		confPath := fmt.Sprintf("%s/tor_%d.conf", torData, i)
		addr := fmt.Sprintf("127.0.0.1:%d", 17001+i)
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
	<- make(chan bool)
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	var (
		tor	string
		torData string
		num int
		metasocks Metasocks
	)

	flag.StringVar(&tor, "tor", "tor", "path to tor executable")
	flag.StringVar(&torData, "tor-data", "data", "path to tor data dirs")
	flag.IntVar(&num, "num", 10, "number of runned tor instances")
	flag.Parse()

	log.Printf("tor:  %s", tor)
	log.Printf("num:  %d", num)

	log.Printf("Multisocks starting...")
	metasocks.Run(tor, torData, num)
}