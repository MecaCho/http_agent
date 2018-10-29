package config

import "flag"

var (
	ServerIP   string
	ServerPort int
	EMUrl      string
	CertData string
	KeyData string
)

func init() {

	flag.StringVar(&ServerIP, "ServerIP", "127.0.0.1", "http server ip")
	flag.IntVar(&ServerPort, "ServerPort", 8143, "http server port")
	flag.StringVar(&EMUrl, "EMUrl", "http://192.145.62.56:8143", "em url")
	flag.StringVar(&CertData, "cert", "xxx", "em url")
	flag.StringVar(&KeyData, "key", "xxxx", "em url")
	flag.Parse()
}
