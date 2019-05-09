package node_install_agent


import (
	"bytes"
	"fmt"
	"glog"
	"httprouter"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"strings"
	"time"
)

const upgradeURLinEdgeMgr = "%s/edgemgr_internal/nodes/%s/installinfo"

// serverResponse is a wrapper for http API responses.
type serverResponse struct {
	body       []byte
	header     http.Header
	statusCode int
	reqURL     *url.URL
}

type header map[string][]string

//InstallInfoResponse node install get plan version from edgemanager
type InstallInfoResponse struct {
	MajorVersion    int                    `json:"major_version"`
	MinorVersion    int                    `json:"minor_version"`
	FixVersion      int                    `json:"fix_version"`
	BuildVersion    string                 `json:"build_version"`
	Package         string                 `json:"package"`
	StartParameters map[string]interface{} `json:"start_parameters"`
}

//NodeInstallStatusPut node install put upgrade result to edgemanager
type NodeInstallStatusPut struct {
	MajorVersion string `json:"major_version"`
	MinorVersion string `json:"minor_version"`
	FixVersion   string `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
}

type upgradeHandle func(http.ResponseWriter, *http.Request, string)

//CheckAuthorition check the cert of request
func (ph *Handle) CheckAuthorition(this upgradeHandle) httprouter.Handle {
	return func(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
		err := ph.validateCert(r)
		if err != nil {
			glog.Error(err.Error())
			http.Error(w, "No TLS certificate provided", http.StatusUnauthorized)
			return
		}
		name, projectID, err := getUserInfoFromCert(r.TLS.PeerCertificates[0])
		if err != nil {
			glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, err.Error())
			http.Error(w, "No Project info in certificate", http.StatusBadRequest)
			return
		}
		nodeID := p.ByName("node_id")
		if nodeID != name {
			glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, "NodeID NOT MATCH")
			http.Error(w, "Node Cert NOT MATCH", http.StatusBadRequest)
			return
		}
		projectIDinURL := p.ByName("project_id")
		if (projectIDinURL != "") && (projectIDinURL != projectID) {
			glog.Errorf("Check user info error, project: %s, id: %s, error message: %s", projectID, name, "ProjectID NOT MATCH")
			http.Error(w, "Project Cert NOT MATCH", http.StatusBadRequest)
			return
		}
		var url string
		if strings.Contains(r.URL.Path, "nodestatus") {
			url = fmt.Sprintf(upgradeURLinEdgeMgr+"/nodestatus", projectID, nodeID)
		} else {
			installType := p.ByName("type")
			if (installType != "install") && (installType != "upgrade") {
				glog.Errorf("Get install plan version info error, project: %s, id: %s, error message: %s", projectID, name, "WRONG INSTALL TYPE")
				http.Error(w, "Request Path Type NOT TMATCh", http.StatusBadRequest)
				return
			}
			url = fmt.Sprintf(upgradeURLinEdgeMgr+"/%s", projectID, nodeID, installType)
		}
		glog.Infof("Request url : %s", url)
		this(w, r, url)
	}
}

//logUpgrade log of upgrade action
func logUpgrade(this upgradeHandle) upgradeHandle {
	return func(w http.ResponseWriter, r *http.Request, url string) {
		funcName := runtime.FuncForPC(reflect.ValueOf(this).Pointer()).Name()
		glog.Infoln("Node upgrade func called : ", funcName)
		glog.Infof("Request from edged host: %s", r.Host)
		this(w, r, url)
	}
}

func (ph *Handle) installerGetNodeSatus(w http.ResponseWriter, r *http.Request, url string) {
	url = strings.Join([]string{ph.edgeAPIURL, ph.edgeAPIVersion, url}, "/")
	req, err := buildRequest(http.MethodGet, url, nil, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Installer node status ret, response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func (ph *Handle) installerPutResult(w http.ResponseWriter, r *http.Request, upgradeURLinEdgeMgr string) {
	upgradeURLinEdgeMgr = strings.Join([]string{ph.edgeAPIURL, ph.edgeAPIVersion, upgradeURLinEdgeMgr}, "/")
	req, err := buildRequest(http.MethodPut, upgradeURLinEdgeMgr, r.Body, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Installer put upgrade result , response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func (ph *Handle) installerGetVersion(w http.ResponseWriter, r *http.Request, upgradeURLinEdgeMgr string) {
	upgradeURLinEdgeMgr = strings.Join([]string{ph.edgeAPIURL, ph.edgeAPIVersion, upgradeURLinEdgeMgr}, "/")
	req, err := buildRequest(http.MethodPost, upgradeURLinEdgeMgr, r.Body, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resp, err := doRequest(&ph.edgeClient, req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(resp.statusCode)
	glog.Infoln("Installer get plan version , response :", string(resp.body))
	fmt.Fprintf(w, string(resp.body))
}

func buildRequest(method, path string, body io.Reader, headers header) (req *http.Request, err error) {
	if body != nil {
		requestBody, err := ioutil.ReadAll(body)
		if err != nil {
			glog.Errorf("Build request error, %s", err.Error())
			return nil, err
		}
		body = bytes.NewReader(requestBody)
	}
	expectedPayload := (method == "POST" || method == "PUT")
	if expectedPayload && body == nil {
		body = bytes.NewReader([]byte{})
	}
	req, err = http.NewRequest(method, path, body)
	if err != nil {
		return nil, err
	}
	if headers != nil {
		for k, v := range headers {
			req.Header[k] = v
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Auth-Token", IAMToken)
	glog.Infoln("New request : ", path)
	return
}

func doRequest(cli *http.Client, r *http.Request) (serverResp serverResponse, err error) {
	serverResp = serverResponse{statusCode: -1}
	resp, err := cli.Do(r)
	if err != nil {
		glog.Errorf("Do request error, %s", err.Error())
		return serverResp, err
	}
	serverResp.statusCode = resp.StatusCode
	defer resp.Body.Close()
	serverResp.body, err = ioutil.ReadAll(resp.Body)
	if err != nil {
		glog.Errorln("Get response error", err.Error())
		return serverResp, err
	}
	return
}

// StartServer starts the node_install_agent service
func StartServer() {
	//cert, err := tls.X509KeyPair(config.CertData, config.KeyData)
	//if err != nil {
	//	panic(err)
	//}
	//tlsConfig := tls.Config{
	//	ClientCAs:    ph.verifyOpts.Roots,
	//	ClientAuth:   tls.RequestClientCert,
	//	Certificates: []tls.Certificate{cert},
	//}

	edgeMgrClient := http.Client{
		Timeout: time.Second * 30,
	}

	ag := InstallAgent{
		edgeMgrClient,
	}

	muxNew := httprouter.New()
	muxNew.POST("/v1/placement_external/nodes/:node_id/installinfo/:type", ph.CheckAuthorition(logUpgrade(ph.installerGetVersion)))
	muxNew.PUT("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ph.CheckAuthorition(logUpgrade(ph.installerPutResult)))
	muxNew.GET("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ph.CheckAuthorition(logUpgrade(ph.installerGetNodeSatus)))

	s := &http.Server{
		Addr:    fmt.Sprintf("%s:%d", config.ServerIP, config.ServerPort),
		Handler: muxNew,
		//TLSConfig:   &tlsConfig,
		IdleTimeout: 30 * time.Second,
		//ErrorLog:    log.New(&filterWriter{}, "", log.LstdFlags),
	}
	glog.Info("Start placement server")
	//s.ListenAndServeTLS("", "")
	s.ListenAndServe()
}
