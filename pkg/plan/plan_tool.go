package plan

import (
	"net/http"
	"flag"
	"glog"
	"time"
)

var pt IPlanTool

type IPlanTool interface {
	CreatePlan() error
	ListPlans() error
	DeletePlan(plan_id string) error
}

type PlanClient struct {
	emClient *http.Client
	ProjectID string
	Token string
}

func NewPlanClient(token string, projectID string, emclient *http.Client) IPlanTool{
	 pt = &PlanClient{
		emClient:emclient,
		ProjectID:projectID,
		Token:token,
	}
	return pt
}

func (this *PlanClient)CreatePlan() error {

	return nil
}

func (this *PlanClient)ListPlans() error {
	glog.Info()
	return nil
}

func (this *PlanClient)DeletePlan(plan_id string) error {

	return nil
}

func newRequest(url string, header http.Header, body map[string]interface{}) error {
	req, err := http.NewRequest(http.MethodGet, url, body)
	if err != nil {
		glog.Errorf("Put node install status error, %s", err)
		return err
	}
	req.Header.Set("Content-type", "application/json;charset=utf8")
	return nil
}

func doRequest(r http.Request) error {

	return nil
}

func main(){
	var operatin string
	var resource string
	var resourceName string
	flag.StringVar(&operatin, "op", "list", "operation")
	flag.StringVar(&resource, "res", "plan", "resource type")
	flag.StringVar(&resourceName, "name", "plan_id", "resourceName")
	flag.Parse()
	newClient := http.Client{
		Timeout:30*time.Second,
	}
	if resource == "plan"{
		pt := NewPlanClient("", "", &newClient)
		if operatin == "list"{
			pt.ListPlans()
		}
		if operatin == "create"{
			pt.CreatePlan()
		}
		if operatin == "delete"{
			pt.DeletePlan(resourceName)
		}
	}

}