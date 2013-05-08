// Copyright 2013 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package docker

import (
	"bytes"
	"fmt"
	etesting "github.com/globocom/tsuru/exec/testing"
	"github.com/globocom/tsuru/log"
	"github.com/globocom/tsuru/provision"
	"github.com/globocom/tsuru/testing"
	"labix.org/v2/mgo/bson"
	"launchpad.net/gocheck"
	stdlog "log"
	"net"
	"runtime"
	"strings"
	"time"
)

func (s *S) TestShouldBeRegistered(c *gocheck.C) {
	p, err := provision.Get("docker")
	c.Assert(err, gocheck.IsNil)
	c.Assert(p, gocheck.FitsTypeOf, &DockerProvisioner{})
}

func (s *S) TestProvisionerProvision(c *gocheck.C) {
	app := testing.NewFakeApp("myapp", "python", 1)
	var p DockerProvisioner
	err := p.Provision(app)
	c.Assert(err, gocheck.IsNil)
}

func (s *S) TestProvisionerRestartCallsDockerStopAndDockerStart(c *gocheck.C) {
	id := "caad7bbd5411"
	fexec := &etesting.FakeExecutor{Output: map[string][]byte{"*": []byte(id)}}
	execut = fexec
	defer func() {
		execut = nil
	}()
	var p DockerProvisioner
	app := testing.NewFakeApp("almah", "static", 1)
	u := provision.Unit{Name: id, AppName: app.GetName(), Type: app.GetPlatform(), Status: provision.StatusInstalling}
	err := collection().Insert(u)
	c.Assert(err, gocheck.IsNil)
	err = p.Restart(app)
	c.Assert(err, gocheck.IsNil)
	args := []string{"stop", id}
	c.Assert(fexec.ExecutedCmd("docker", args), gocheck.Equals, true)
	args = []string{"start", id}
	c.Assert(fexec.ExecutedCmd("docker", args), gocheck.Equals, true)
}

func (s *S) TestDeployShouldCallDockerCreate(c *gocheck.C) {
	out := `
    {
            "NetworkSettings": {
            "IpAddress": "10.10.10.10",
            "IpPrefixLen": 8,
            "Gateway": "10.65.41.1",
            "PortMapping": {}
    }
}`
	fexec := &etesting.FakeExecutor{Output: map[string][]byte{"*": []byte(out)}}
	execut = fexec
	defer func() {
		execut = nil
	}()
	p := DockerProvisioner{}
	app := testing.NewFakeApp("cribcaged", "python", 1)
	w := &bytes.Buffer{}
	err := p.Deploy(app, w)
	defer p.Destroy(app)
	c.Assert(err, gocheck.IsNil)
	image := fmt.Sprintf("%s/python", s.repoNamespace)
	appRepo := fmt.Sprintf("git://%s/cribcaged.git", s.gitHost)
	containerCmd := fmt.Sprintf("/var/lib/tsuru/deploy %s && %s %s", appRepo, s.runBin, s.runArgs)
	args := []string{"run", "-d", "-t", "-p", s.port, image, "/bin/bash", "-c", containerCmd}
	c.Assert(fexec.ExecutedCmd("docker", args), gocheck.Equals, true)
}

func (s *S) TestProvisionerDestroy(c *gocheck.C) {
	fexec := &etesting.FakeExecutor{}
	execut = fexec
	defer func() {
		execut = nil
	}()
	w := new(bytes.Buffer)
	l := stdlog.New(w, "", stdlog.LstdFlags)
	log.SetLogger(l)
	app := testing.NewFakeApp("myapp", "python", 1)
	u := provision.Unit{
		Name:       app.ProvisionUnits()[0].GetName(),
		AppName:    app.GetName(),
		Machine:    app.ProvisionUnits()[0].GetMachine(),
		InstanceId: app.ProvisionUnits()[0].GetInstanceId(),
		Status:     provision.StatusCreating,
	}
	err := s.conn.Collection(s.collName).Insert(&u)
	c.Assert(err, gocheck.IsNil)
	img := image{Name: app.GetName()}
	err = s.conn.Collection(s.imageCollName).Insert(&img)
	c.Assert(err, gocheck.IsNil)
	var p DockerProvisioner
	c.Assert(p.Destroy(app), gocheck.IsNil)
	ok := make(chan bool, 1)
	go func() {
		for {
			coll := s.conn.Collection(s.collName)
			ct, err := coll.Find(bson.M{"name": u.Name}).Count()
			if err != nil {
				c.Fatal(err)
			}
			if ct == 0 {
				ok <- true
				return
			}
			runtime.Gosched()
		}
	}()
	select {
	case <-ok:
	case <-time.After(10e9):
		c.Error("Timed out waiting for the container to be destroyed (10 seconds)")
	}
	args := []string{"stop", "i-01"}
	c.Assert(fexec.ExecutedCmd("docker", args), gocheck.Equals, true)
	args = []string{"rm", "i-01"}
	c.Assert(fexec.ExecutedCmd("docker", args), gocheck.Equals, true)
}

func (s *S) TestProvisionerAddr(c *gocheck.C) {
	var p DockerProvisioner
	app := testing.NewFakeApp("myapp", "python", 1)
	addr, err := p.Addr(app)
	c.Assert(err, gocheck.IsNil)
	c.Assert(addr, gocheck.Equals, app.ProvisionUnits()[0].GetIp())
}

func (s *S) TestProvisionerAddUnits(c *gocheck.C) {
	var p DockerProvisioner
	app := testing.NewFakeApp("myapp", "python", 0)
	units, err := p.AddUnits(app, 2)
	c.Assert(err, gocheck.IsNil)
	c.Assert(units, gocheck.DeepEquals, []provision.Unit{})
}

func (s *S) TestProvisionerRemoveUnit(c *gocheck.C) {
	var p DockerProvisioner
	app := testing.NewFakeApp("myapp", "python", 0)
	err := p.RemoveUnit(app, "")
	c.Assert(err, gocheck.IsNil)
}

func (s *S) TestProvisionerExecuteCommand(c *gocheck.C) {
	var p DockerProvisioner
	app := testing.NewFakeApp("almah", "static", 2)
	var buf bytes.Buffer
	err := p.ExecuteCommand(&buf, &buf, app, "ls", "-lh")
	c.Assert(err, gocheck.IsNil)
}

func (s *S) TestCollectStatus(c *gocheck.C) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	c.Assert(err, gocheck.IsNil)
	defer listener.Close()
	err = collection().Insert(
		provision.Unit{Name: "9930c24f1c5f", AppName: "ashamed", Type: "python"},
		provision.Unit{Name: "9930c24f1c4f", AppName: "make-up", Type: "python"},
	)
	c.Assert(err, gocheck.IsNil)
	defer collection().RemoveAll(bson.M{"name": bson.M{"$in": []string{"9930c24f1c5f", "9930c24f1c4f"}}})
	psOutput := `9930c24f1c5f
9930c24f1c4f
9930c24f1c3f
`
	c1Output := fmt.Sprintf(`{
	"NetworkSettings": {
		"IpAddress": "127.0.0.1",
		"IpPrefixLen": 8,
		"Gateway": "10.65.41.1",
		"PortMapping": {
			"%s": "90293"
		}
	}
}`, strings.Split(listener.Addr().String(), ":")[1])
	c2Output := `{
	"NetworkSettings": {
		"IpAddress": "127.0.0.1",
		"IpPrefixLen": 8,
		"Gateway": "10.65.41.1",
		"PortMapping": {
			"8889": "90294"
		}
	}
}`
	expected := []provision.Unit{
		{
			Name:    "9930c24f1c5f",
			AppName: "ashamed",
			Type:    "python",
			Machine: 0,
			Ip:      "127.0.0.1",
			Status:  provision.StatusStarted,
		},
		{
			Name:    "9930c24f1c4f",
			AppName: "make-up",
			Type:    "python",
			Machine: 0,
			Ip:      "127.0.0.1",
			Status:  provision.StatusInstalling,
		},
	}
	output := map[string][]byte{
		"ps -q":                []byte(psOutput),
		"inspect 9930c24f1c5f": []byte(c1Output),
		"inspect 9930c24f1c4f": []byte(c2Output),
	}
	fexec := &etesting.FakeExecutor{Output: output}
	execut = fexec
	defer func() {
		execut = nil
	}()
	var p DockerProvisioner
	units, err := p.CollectStatus()
	c.Assert(err, gocheck.IsNil)
	if units[0].Name != expected[0].Name {
		units[0], units[1] = units[1], units[0]
	}
	c.Assert(units, gocheck.DeepEquals, expected)
}

func (s *S) TestProvisionCollection(c *gocheck.C) {
	collection := collection()
	c.Assert(collection.Name, gocheck.Equals, s.collName)
}
