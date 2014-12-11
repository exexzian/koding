package koding

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"koding/db/mongodb"

	amazonClient "koding/kites/kloud/api/amazon"
	"koding/kites/kloud/eventer"
	"koding/kites/kloud/klient"
	"koding/kites/kloud/kloud"
	"koding/kites/kloud/machinestate"
	"koding/kites/kloud/multiec2"
	"koding/kites/kloud/protocol"
	"koding/kites/kloud/provider/amazon"

	"github.com/koding/kite"
	"github.com/koding/logging"
	"github.com/koding/metrics"
	"github.com/mitchellh/goamz/ec2"
)

const (
	ProviderName = "koding"
)

type pushValues struct {
	Start, Finish int
}

// Provider implements the kloud packages Storage, Builder and Controller
// interface
type Provider struct {
	Kite *kite.Kite
	Log  logging.Logger
	Push func(string, int, machinestate.State)

	// DB reference
	Session       *mongodb.MongoDB
	DomainStorage protocol.DomainStorage

	// A flag saying if user permissions should be ignored
	// store negation so default value is aligned with most common use case
	Test bool

	// AWS related references and settings
	EC2Clients *multiec2.Clients
	DNS        *DNS
	Bucket     *Bucket

	KontrolURL        string
	KontrolPrivateKey string
	KontrolPublicKey  string

	// If available a key pair with the given public key and name should be
	// deployed to the machine, the corresponding PrivateKey should be returned
	// in the ProviderArtifact. Some providers such as Amazon creates
	// publicKey's on the fly and generates the privateKey themself.
	PublicKey  string `structure:"publicKey"`
	PrivateKey string `structure:"privateKey"`
	KeyName    string `structure:"keyName"`

	// A set of connected, ready to use klients
	KlientPool *klient.KlientPool

	// A set of machines that defines machines who's klient kites are not
	// running. The timer is used to stop the machines after 30 minutes
	// inactivity.
	InactiveMachines   map[string]*time.Timer
	InactiveMachinesMu sync.Mutex

	PlanChecker func(*protocol.Machine) (Checker, error)

	Stats *metrics.DogStatsD
}

func (p *Provider) NewClient(m *protocol.Machine) (*amazon.AmazonClient, error) {
	a := &amazon.AmazonClient{
		Log: p.Log,
		Push: func(msg string, percentage int, state machinestate.State) {
			p.Log.Debug("[%s] %s (username: %s)", m.Id, msg, m.Username)

			m.Eventer.Push(&eventer.Event{
				Message:    msg,
				Status:     state,
				Percentage: percentage,
			})
		},
		Metrics: p.Stats,
	}

	var err error

	// we pass a nil client just to fill the Builder data. The reason for that
	// is to retrieve the `region` of a user so we can create a client based on
	// the region below.
	a.Amazon, err = amazonClient.New(m.Builder, nil)
	if err != nil {
		return nil, fmt.Errorf("koding-amazon err: %s", err)
	}

	if a.Builder.Region == "" {
		a.Builder.Region = "us-east-1"
		a.Log.Critical("[%s] region is not set in. Fallback to us-east-1.", m.Id)
	}

	client, err := p.EC2Clients.Region(a.Builder.Region)
	if err != nil {
		return nil, err
	}

	a.Client = client

	// needed to deploy during build
	a.Builder.KeyPair = p.KeyName

	// needed to create the keypair if it doesn't exist
	a.Builder.PublicKey = p.PublicKey
	a.Builder.PrivateKey = p.PrivateKey
	return a, nil
}

func (p *Provider) Start(m *protocol.Machine) (*protocol.Artifact, error) {
	checker, err := p.PlanChecker(m)
	if err != nil {
		return nil, err
	}

	if err := checker.AlwaysOn(); err != nil {
		return nil, err
	}

	a, err := p.NewClient(m)
	if err != nil {
		return nil, err
	}

	infoResp, err := a.Info()
	if err != nil {
		return nil, err
	}

	artifact := &protocol.Artifact{
		IpAddress:  m.IpAddress,
		InstanceId: a.Builder.InstanceId,
	}

	// stop the timer and remove it from the list of inactive machines so it
	// doesn't get called later again.
	p.stopTimer(m)

	a.Push("Starting machine", 10, machinestate.Starting)

	// check if the user has something else than their current instance type
	// and revert back to t2.micro. This is lazy auto healing of instances that
	// were created because there were no capacity for their specific instance
	// type.
	if infoResp.InstanceType != a.Builder.InstanceType {
		a.Log.Warning("[%s] instance is using '%s'. Changing back to '%s'",
			m.Id, infoResp.InstanceType, a.Builder.InstanceType)

		opts := &ec2.ModifyInstance{InstanceType: instances[a.Builder.InstanceType].String()}

		if _, err := a.Client.ModifyInstance(a.Builder.InstanceId, opts); err != nil {
			p.Log.Warning("[%s] couldn't change instance to '%s' again. err: %s",
				m.Id, a.Builder.InstanceType, err)
		}

		// Because of AWS's eventually consistency state, we wait to get the
		// final and correct answer.
		time.Sleep(time.Second * 2)
	}

	// only start if the machine is stopped, stopping
	if infoResp.State.In(machinestate.Stopped, machinestate.Stopping) {
		// Give time until it's being stopped
		if infoResp.State == machinestate.Stopping {
			time.Sleep(time.Second * 20)
		}

		startFunc := func() error {
			artifact, err = a.Start(true)
			if err == nil {
				return nil
			}

			// check if the error is a 'InsufficientInstanceCapacity" error or
			// "InstanceLimitExceeded, if not return back because it's not a
			// resource or capacity problem.
			if !isCapacityError(err) {
				return err
			}

			p.Log.Error("[%s] IMPORTANT: %s", m.Id, err)

			for _, instanceType := range InstancesList {
				p.Log.Warning("[%s] Fallback: starting again with using instance: %s instead of %s",
					m.Id, instanceType, a.Builder.InstanceType)

				// now change the instance type before we start so we can
				// avoid the instance capacity problem
				opts := &ec2.ModifyInstance{InstanceType: instanceType}
				if _, err := a.Client.ModifyInstance(a.Builder.InstanceId, opts); err != nil {
					return err
				}

				// just give a little time so it can be catched because EC2 is
				// eventuall consistent. Otherwise start might fail even if we
				// do the ModifyInstance call
				time.Sleep(time.Second * 2)

				artifact, err = a.Start(true)
				if err == nil {
					return nil
				}

				p.Log.Warning("[%s] Fallback: couldn't start instance with type: '%s'. err: %s ",
					m.Id, instanceType, err)
			}

			return errors.New("no other instances are available")
		}

		// go go go!
		if err := startFunc(); err != nil {
			return nil, err
		}
	}

	// update the intermediate information
	p.Update(m.Id, &kloud.StorageData{
		Type: "starting",
		Data: map[string]interface{}{
			"ipAddress": artifact.IpAddress,
		},
	})

	a.Push("Initializing domain instance", 65, machinestate.Starting)
	if err := p.UpdateDomain(artifact.IpAddress, m.Domain.Name, m.Username); err != nil {
		p.Log.Error("[%s] updating domains for starting err: %s", m.Id, err.Error())
	}

	// also get all domain aliases that belongs to this machine and unset
	a.Push("Updating domain aliases", 80, machinestate.Starting)
	domains, err := p.DomainStorage.GetByMachine(m.Id)
	if err != nil {
		p.Log.Error("[%s] fetching domains for starting err: %s", m.Id, err.Error())
	}

	for _, domain := range domains {
		if err := p.UpdateDomain(artifact.IpAddress, domain.Name, m.Username); err != nil {
			p.Log.Error("[%s] couldn't update domain: %s", m.Id, err.Error())
		}
	}

	artifact.DomainName = m.Domain.Name

	a.Push("Checking remote machine", 90, machinestate.Starting)
	if p.IsKlientReady(m.QueryString) {
		p.Log.Debug("[%s] klient is ready.", m.Id)
	} else {
		p.Log.Warning("[%s] klient is not ready. I couldn't connect to it.", m.Id)
	}

	return artifact, nil
}

func (p *Provider) Stop(m *protocol.Machine) error {
	a, err := p.NewClient(m)
	if err != nil {
		return err
	}

	// stop the timer and remove it from the list of inactive machines so it
	// doesn't get called later again.
	p.stopTimer(m)

	err = a.Stop(true)
	if err != nil {
		return err
	}

	a.Push("Initializing domain instance", 65, machinestate.Stopping)
	if err := p.DNS.Validate(m.Domain.Name, m.Username); err != nil {
		return err
	}

	a.Push("Deleting domain", 85, machinestate.Stopping)
	if err := p.DNS.Delete(m.Domain.Name, m.IpAddress); err != nil {
		p.Log.Warning("[%s] couldn't delete domain %s", m.Id, err)
	}

	// also get all domain aliases that belongs to this machine and unset
	domains, err := p.DomainStorage.GetByMachine(m.Id)
	if err != nil {
		p.Log.Error("[%s] fetching domains for unseting err: %s", m.Id, err.Error())
	}

	for _, domain := range domains {
		if err := p.DNS.Delete(domain.Name, m.IpAddress); err != nil {
			p.Log.Error("[%s] couldn't delete domain: %s", m.Id, err.Error())
		}
	}

	return nil
}

func (p *Provider) Restart(m *protocol.Machine) error {
	a, err := p.NewClient(m)
	if err != nil {
		return err
	}

	return a.Restart(false)
}

func (p *Provider) Reinit(m *protocol.Machine) (*protocol.Artifact, error) {
	a, err := p.NewClient(m)
	if err != nil {
		return nil, err
	}

	if err := p.destroy(a, m, &pushValues{Start: 10, Finish: 40}); err != nil {
		return nil, err
	}

	// clean up old data, so if build fails below at least we give the chance to build it again
	p.Update(m.Id, &kloud.StorageData{
		Type: "building",
		Data: map[string]interface{}{
			"instanceId":   "",
			"ipAddress":    "",
			"instanceName": "",
			"queryString":  "",
		},
	})
	p.UpdateState(m.Id, "Reinit cleanup", machinestate.NotInitialized)

	// cleanup this too so "build" can continue with a clean setup
	a.Builder.InstanceId = ""
	m.QueryString = ""
	m.IpAddress = ""

	b := &Build{
		amazon:   a,
		machine:  m,
		provider: p,
		start:    40,
		finish:   90,
		log:      p.Log,
	}

	// this updates/creates domain
	return b.run()
}

func (p *Provider) Destroy(m *protocol.Machine) error {
	a, err := p.NewClient(m)
	if err != nil {
		return err
	}

	if err := p.destroy(a, m, &pushValues{Start: 10, Finish: 80}); err != nil {
		return err
	}

	domains, err := p.DomainStorage.GetByMachine(m.Id)
	if err != nil {
		p.Log.Error("[%s] fetching domains for unseting err: %s", m.Id, err.Error())
	}

	a.Push("Deleting base domain", 85, machinestate.Terminating)
	if err := p.DNS.Delete(m.Domain.Name, m.IpAddress); err != nil {
		// if it's already deleted, for example because of a STOP, than we just
		// log it here instead of returning the error
		p.Log.Error("[%s] deleting domain during destroying err: %s", m.Id, err.Error())
	}

	a.Push("Deleting custom domain", 90, machinestate.Terminating)
	for _, domain := range domains {
		if err := p.DNS.Delete(domain.Name, m.IpAddress); err != nil {
			p.Log.Error("[%s] couldn't delete domain: %s", m.Id, err.Error())
		}

		if err := p.DomainStorage.UpdateMachine(domain.Name, ""); err != nil {
			p.Log.Error("[%s] couldn't unset machine domain: %s", m.Id, err.Error())
		}
	}

	return nil
}

func (p *Provider) destroy(a *amazon.AmazonClient, m *protocol.Machine, v *pushValues) error {
	// stop the timer and remove it from the list of inactive machines so it
	// doesn't get called later again.
	p.stopTimer(m)

	// means if final is 40 our destroy method below will be push at most up to
	// 32.
	middleVal := float64(v.Finish) * (8.0 / 10.0)
	return a.Destroy(v.Start, int(middleVal))
}

// stopTimer stops the inactive timeout timer for the given queryString
func (p *Provider) stopTimer(m *protocol.Machine) {
	// stop the timer and remove it from the list of inactive machines so it
	// doesn't get called later again.
	p.InactiveMachinesMu.Lock()
	if timer, ok := p.InactiveMachines[m.QueryString]; ok {
		p.Log.Info("[%s] stopping inactive machine timer %s", m.Id, m.QueryString)
		timer.Stop()
		p.InactiveMachines[m.QueryString] = nil // garbage collect
		delete(p.InactiveMachines, m.QueryString)
	}
	p.InactiveMachinesMu.Unlock()
}

// startTimer starts the inactive timeout timer for the given queryString. It
// stops the machine after 30 minutes.
func (p *Provider) startTimer(curMachine *protocol.Machine) {
	if a, ok := curMachine.Builder["alwaysOn"]; ok {
		if isAlwaysOn, ok := a.(bool); ok && isAlwaysOn {
			return // don't stop if alwaysOn is enabled
		}
	}

	p.InactiveMachinesMu.Lock()
	_, ok := p.InactiveMachines[curMachine.QueryString]
	p.InactiveMachinesMu.Unlock()
	if ok {
		// just return, because it's already in the map so it will be expired
		// with the function below
		return
	}

	p.Log.Info("[%s] klient is not running (username: %s), adding to list of inactive machines.",
		curMachine.Id, curMachine.Username)

	stopAfter := time.Minute * 30

	// wrap it so we can return errors and log them
	stopFunc := func(id string) error {
		// fetch it again so we have always the latest data. This is important
		// because another kloud instance might already stopped or we have
		// again a connection to klient
		m, err := p.Get(id)
		if err != nil {
			return err
		}

		// add fake eventer to avoid panic errors on NewClient at provider
		m.Eventer = &eventer.Events{}

		a, err := p.NewClient(m)
		if err != nil {
			return err
		}

		p.Log.Info("[%s] 30 minutes passed. Rechecking again before I stop the machine (username: %s)",
			m.Id, m.Username)

		infoResp, err := a.Info()
		if err != nil {
			return err
		}

		if infoResp.State.InProgress() {
			return fmt.Errorf("machine is in progress of '%s'", infoResp.State)
		}

		if infoResp.State == machinestate.Stopped {
			p.Log.Info("[%s] stop timer aborting. Machine is already stopped (username: %s)",
				m.Id, m.Username)
			return errors.New("machine is already stopped")
		}

		if infoResp.State == machinestate.Running {
			err := klient.Exists(p.Kite, m.QueryString)
			if err == nil {
				p.Log.Info("[%s] stop timer aborting. Klient is already running (username: %s)",
					m.Id, m.Username)
				return errors.New("we have a klient connection")
			}
		}

		p.Lock(m.Id)
		defer p.Unlock(m.Id)

		// mark our state as stopping so others know what we are doing
		stoppingReason := "Stopping process started due not active klient after 30 minutes waiting."
		p.UpdateState(m.Id, stoppingReason, machinestate.Stopping)

		p.Log.Info("[%s] Stopping machine (username: %s) after 30 minutes klient disconnection.",
			m.Id, m.Username)

		// Hasta la vista, baby!
		if err := p.Stop(m); err != nil {
			p.Log.Warning("[%s] could not stop ghost machine %s", m.Id, err)
		}

		// update to final state too
		stopReason := "Stopping due not active and unreachable klient after 30 minutes waiting."
		p.UpdateState(m.Id, stopReason, machinestate.Stopped)

		// we don't need it anymore
		p.InactiveMachinesMu.Lock()
		delete(p.InactiveMachines, m.QueryString)
		p.InactiveMachinesMu.Unlock()

		return nil
	}

	p.InactiveMachines[curMachine.QueryString] = time.AfterFunc(stopAfter, func() {
		if err := stopFunc(curMachine.Id); err != nil {
			p.Log.Error("[%s] inactive klient stopper err: %s", curMachine.Id, err)
		}
	})
}
