package node_install_agent

import (
	"net/http"
	"fmt"
	"time"
	"io/ioutil"
	"io"
	"bytes"
	"encoding/json"
	"httprouter"
	"glog"
	"node_install_agent/config"
)

type InstallInfoResponse struct {
	MajorVersion int `json:"major_version"`
	MinorVersion int `json:"minor_version"`
	FixVersion int `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Package string `json:"package"`
	StartParameters map[string]interface{} `json:"start_parameters"`
}

type NodeInstallStatusPut struct {
	MajorVersion string `json:"major_version"`
	MinorVersion string `json:"minor_version"`
	FixVersion   string `json:"fix_version"`
	BuildVersion string `json:"build_version"`
	Status       string `json:"status"`
	Reason       string `json:"reason"`
}

type InstallAgent struct {
	EMClient http.Client
	ProjectID string
	Name string
	PH string
}



func (ag *InstallAgent)EdgeInstallPost(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	installType := p.ByName("type")
	glog.Infof("Get node install info request from edged host: %s", r.Host)
	projectID := ag.ProjectID
	urlEdgeMgr := fmt.Sprintf("%s/v1/%s/edgemgr_internal/nodes/%s/installinfo/%s", config.EMUrl, projectID, nodeID, installType)
	requestBody, err := ioutil.ReadAll(r.Body)
	if err != nil{
		glog.Errorf("")
		return
	}
	reader := bytes.NewReader(requestBody)
	req, err := http.NewRequest(http.MethodPost, urlEdgeMgr, reader)
	fmt.Println(urlEdgeMgr, string(requestBody))
	if err != nil{
		glog.Errorln("")
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	resp, err := ag.EMClient.Do(req)
	if err != nil{
		glog.Errorln("")
		return
	}

	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	if resp.StatusCode != http.StatusOK{
		if resp.StatusCode == http.StatusInternalServerError{
			http.Error(w, "fail to update master address", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(resp.StatusCode)
		body := make(map[string]interface{})
		fmt.Println(string(respContent))
		err = json.Unmarshal(respContent, &body)
		if err != nil{
			glog.Errorf("Get node install info error, %s", err)
			return
		}
		fmt.Printf("%#v\n", body)
		ret, err := json.Marshal(body)
		if err != nil{
			glog.Errorf("Get node install info error, %s", err)
			return
		}
		io.WriteString(w, string(ret))
		return
	}

	var body InstallInfoResponse
	fmt.Println(string(respContent))
	err = json.Unmarshal(respContent, &body)
	if err != nil{
		glog.Errorf("Get node install info error, %s", err)
		return
	}
	fmt.Printf("%#v\n", body)
	ret, err := json.Marshal(body)
	if err != nil{
		glog.Errorf("Get node install info error, %s", err)
		return
	}
	io.WriteString(w, string(ret))
}



func (ag *InstallAgent)EdgeInstallPutNodeStatus(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	glog.Infof("Put node install status request from edged host: %s", r.Host)
	projectID := ag.ProjectID

	urlEdgeMgr := fmt.Sprintf("%s/v1/%s/edgemgr_internal/nodes/%s/installinfo/nodestatus", config.EMUrl, projectID, nodeID)
	requestBody, err := ioutil.ReadAll(r.Body)
	if err != nil{
		glog.Errorf("")
		return
	}
	reader := bytes.NewReader(requestBody)
	req, err := http.NewRequest(http.MethodPut, urlEdgeMgr, reader)
	glog.Errorf("url ; %#v , Body : %#v", urlEdgeMgr, string(requestBody))
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	resp, err := ag.EMClient.Do(req)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	io.WriteString(w, string(respContent))
}


func (ag *InstallAgent)EdgeInstallGetNodeStatus(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("node_id")
	glog.Infof("Get node install status request from edged host: %s", r.Host)
	projectID := ag.ProjectID

	urlEdgeMgr := fmt.Sprintf("%s/v1/%s/edgemgr_internal/nodes/%s/installinfo/nodestatus", config.EMUrl, projectID, nodeID)
	req, err := http.NewRequest(http.MethodGet, urlEdgeMgr, nil)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	resp, err := ag.EMClient.Do(req)
	if err != nil{
		glog.Errorf("Put node install status error, %s", err)
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	respContent, err := ioutil.ReadAll(resp.Body)
	if err != nil{
		return
	}
	io.WriteString(w, string(respContent))
}

//
func (ag *InstallAgent)EdgePost(w http.ResponseWriter, r *http.Request, p httprouter.Params) {
	nodeID := p.ByName("post_node_id")
	projectID := p.ByName("project_id")
	glog.Infof("Get node install info request from edged host: %s", r.Host)
	urlEdgeMgr := fmt.Sprintf("%s/v1/%s/edgemgr/nodes/%s/upgrade", config.EMUrl, projectID, nodeID)
	req, err := http.NewRequest(http.MethodPost, urlEdgeMgr, nil)
	fmt.Println(urlEdgeMgr)
	if err != nil{
		glog.Errorln("")
		return
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	req.Header.Set("X-Auth-Token", "token")
	resp, err := ag.EMClient.Do(req)
	if err != nil{
		glog.Errorln("")
		return
	}
	if resp.StatusCode != http.StatusOK{
		return
	}
	defer resp.Body.Close()
	io.WriteString(w, "success")
}


// StartServer starts the node_install_agent service
func StartServer() {
	//cert, err := tls.X509KeyPair(config.certData, config.keyData)
	//if err != nil {
	//	panic(err)
	//}
	//tlsConfig := tls.Config{
	//	ClientCAs:    ph.verifyOpts.Roots,
	//ClientAuth:   tls.RequestClientCert,
	//Certificates: []tls.Certificate{cert},
	//}

	edgeMgrClient := http.Client{
		Timeout:time.Second * 30,
	}

	ag := InstallAgent{
		edgeMgrClient,
		"",
		"",
		"",
	}

	mux_new := httprouter.New()
	mux_new.POST("/v1/placement_external/nodes/:node_id/installinfo/:type", ag.EdgeInstallPost)
	mux_new.PUT("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ag.EdgeInstallPutNodeStatus)
	mux_new.GET("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ag.EdgeInstallGetNodeStatus)
	mux_new.POST("/v1/placement_external/nodes/:node_id/upgrade", ag.EdgePost)


	s := &http.Server{
		Addr:        fmt.Sprintf("%s:%d", config.ServerIP, config.ServerPort),
		Handler:     mux_new,
		//TLSConfig:   &tlsConfig,
		IdleTimeout: 30 * time.Second,
		//ErrorLog:    log.New(&filterWriter{}, "", log.LstdFlags),
	}
	glog.Info("Start placement server")
	//s.ListenAndServeTLS("", "")
	s.ListenAndServe()
}

