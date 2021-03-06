package node_install_agent

import (
	"testing"
	"net/http"
	"fmt"
	"io/ioutil"
	"net/http/httptest"
	"time"
	"httprouter"
	"reflect"
	"strings"
	"monkey"
)

func TestStartServer(t *testing.T) {
	edgeMgrClient := http.Client{
		Timeout: time.Second * 30,
	}
	ag := InstallAgent{
		edgeMgrClient,
	}
	p := httprouter.Params{}
	testMux := httprouter.New()
	testMux.GET("/v1/placement_external/nodes/:node_id/installinfo/nodestatus", ag.EdgeInstallGetNodeStatus)
	w := httptest.NewRecorder()

	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ag.EMClient), "Do")
	monkey.PatchInstanceMethod(reflect.TypeOf(&ag.EMClient), "Do",
		func(_ *http.Client, _ *http.Request) (*http.Response, error) {
			body := "{\"state\":\"UPGRADING\"}"
			resp := http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(strings.NewReader(body))}
			return &resp, nil
		})

	req := httptest.NewRequest("GET", "/v1/placement_external/nodes/:node_id/installinfo/nodestatus", nil)
	ag.EdgeInstallGetNodeStatus(w, req, p)

	resp := w.Result()
	body, _ := ioutil.ReadAll(resp.Body)

	fmt.Println(resp.StatusCode)
	fmt.Println(resp.Header.Get("Content-Type"))
	fmt.Println(string(body))

}

//func TestAgent(t *testing.T) {
//	// var defined here
//	name := "fakeName"
//	project := "fakeProject"
//
//	mqURLForManager := "manager_url"
//	fakeCA := ""
//
//	pool := x509.NewCertPool()
//	pool.AppendCertsFromPEM([]byte(fakeCA))
//	opts := x509.VerifyOptions{
//		Roots: pool,
//		KeyUsages: []x509.ExtKeyUsage{
//			x509.ExtKeyUsageServerAuth,
//			x509.ExtKeyUsageClientAuth},
//	}
//
//
//	ph := InstallAgent{
//		//verifyOpts: opts,
//		//edgeAPIURL: "fake_url",
//	}
//
//	// request to edgemgr error
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, _ string) (resp *http.Response, err error) {
//			return nil, errors.New("mqclient return error")
//		})
//
//	url, _ := ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")
//
//	// request response OK
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, url string) (*http.Response, error) {
//			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
//			resp := http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(strings.NewReader(body))}
//			return &resp, nil
//		})
//
//	url, _ = ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, mqURLForManager, url, "QueryAssignedMQ return is not match")
//
//	// not 200 returned from edgemgr
//	defer monkey.UnpatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get")
//	monkey.PatchInstanceMethod(reflect.TypeOf(&ph.mqClient), "Get",
//		func(_ *http.Client, url string) (*http.Response, error) {
//			body := fmt.Sprintf("{\"mq_url\":\"%s\"}", mqURLForManager)
//			resp := http.Response{StatusCode: http.StatusBadRequest, Body: ioutil.NopCloser(strings.NewReader(body))}
//			return &resp, nil
//		})
//
//	url, _ = ph.QueryAssignedMQ(name, project)
//	AssertStringEqual(t, "", url, "QueryAssignedMQ return is not match")
//}
