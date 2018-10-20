package config

import "flag"

var (
	ServerIP string
	ServerPort int
)


func init() {

	flag.StringVar(&ServerIP, "ServerIP", "127.0.0.1", "http server ip")
	flag.IntVar(&ServerPort, "ServerPort", 8143, "http server port")
	flag.Parse()
}
