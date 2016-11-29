/*
 * Licensed to the Apache Software Foundation (ASF) under one
 * or more contributor license agreements. See the NOTICE file
 * distributed with this work for additional information
 * regarding copyright ownership. The ASF licenses this file
 * to you under the Apache License, Version 2.0 (the
 * "License"); you may not use this file except in compliance
 * with the License. You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

package thrift

import (
	"errors"
	"log"
	"runtime/debug"
	"sync"
	"time"
)

const (
	WAIT_TIME_OUT = 10 * time.Second
)

// Simple, non-concurrent server for testing.
type TSimpleServer struct {
	quit chan struct{}

	cm                     *ConnectionManager
	processorFactory       TProcessorFactory
	serverTransport        TServerTransport
	inputTransportFactory  TTransportFactory
	outputTransportFactory TTransportFactory
	inputProtocolFactory   TProtocolFactory
	outputProtocolFactory  TProtocolFactory
}

func NewTSimpleServer2(processor TProcessor, serverTransport TServerTransport) *TSimpleServer {
	return NewTSimpleServerFactory2(NewTProcessorFactory(processor), serverTransport)
}

func NewTSimpleServer4(processor TProcessor, serverTransport TServerTransport, transportFactory TTransportFactory, protocolFactory TProtocolFactory) *TSimpleServer {
	return NewTSimpleServerFactory4(NewTProcessorFactory(processor),
		serverTransport,
		transportFactory,
		protocolFactory,
	)
}

func NewTSimpleServer6(processor TProcessor, serverTransport TServerTransport, inputTransportFactory TTransportFactory, outputTransportFactory TTransportFactory, inputProtocolFactory TProtocolFactory, outputProtocolFactory TProtocolFactory) *TSimpleServer {
	return NewTSimpleServerFactory6(NewTProcessorFactory(processor),
		serverTransport,
		inputTransportFactory,
		outputTransportFactory,
		inputProtocolFactory,
		outputProtocolFactory,
	)
}

func NewTSimpleServerFactory2(processorFactory TProcessorFactory, serverTransport TServerTransport) *TSimpleServer {
	return NewTSimpleServerFactory6(processorFactory,
		serverTransport,
		NewTTransportFactory(),
		NewTTransportFactory(),
		NewTBinaryProtocolFactoryDefault(),
		NewTBinaryProtocolFactoryDefault(),
	)
}

func NewTSimpleServerFactory4(processorFactory TProcessorFactory, serverTransport TServerTransport, transportFactory TTransportFactory, protocolFactory TProtocolFactory) *TSimpleServer {
	return NewTSimpleServerFactory6(processorFactory,
		serverTransport,
		transportFactory,
		transportFactory,
		protocolFactory,
		protocolFactory,
	)
}

func NewTSimpleServerFactory6(processorFactory TProcessorFactory, serverTransport TServerTransport, inputTransportFactory TTransportFactory, outputTransportFactory TTransportFactory, inputProtocolFactory TProtocolFactory, outputProtocolFactory TProtocolFactory) *TSimpleServer {
	return &TSimpleServer{
		processorFactory:       processorFactory,
		serverTransport:        serverTransport,
		inputTransportFactory:  inputTransportFactory,
		outputTransportFactory: outputTransportFactory,
		inputProtocolFactory:   inputProtocolFactory,
		outputProtocolFactory:  outputProtocolFactory,
		cm:   NewConnectionManager(),
		quit: make(chan struct{}, 1),
	}
}

func (p *TSimpleServer) ProcessorFactory() TProcessorFactory {
	return p.processorFactory
}

func (p *TSimpleServer) ServerTransport() TServerTransport {
	return p.serverTransport
}

func (p *TSimpleServer) InputTransportFactory() TTransportFactory {
	return p.inputTransportFactory
}

func (p *TSimpleServer) OutputTransportFactory() TTransportFactory {
	return p.outputTransportFactory
}

func (p *TSimpleServer) InputProtocolFactory() TProtocolFactory {
	return p.inputProtocolFactory
}

func (p *TSimpleServer) OutputProtocolFactory() TProtocolFactory {
	return p.outputProtocolFactory
}

func (p *TSimpleServer) Listen() error {
	return p.serverTransport.Listen()
}

func (p *TSimpleServer) AcceptLoop() error {
	for {
		client, err := p.serverTransport.Accept()
		if err != nil {
			select {
			case <-p.quit:
				return nil
			default:
			}
			return err
		}
		if client != nil {
			go func() {
				p.cm.Add(1)
				if err := p.processRequests(client); err != nil {
					log.Println("error processing request:", err)
				}
				p.cm.Done()
			}()
		}
	}
}

func (p *TSimpleServer) Serve() error {
	err := p.Listen()
	if err != nil {
		return err
	}
	p.AcceptLoop()
	return nil
}

func (p *TSimpleServer) Stop() error {
	p.quit <- struct{}{}
	p.serverTransport.Interrupt()
	return nil
}

func (p *TSimpleServer) processRequests(client TTransport) error {
	processor := p.processorFactory.GetProcessor(client)
	inputTransport := p.inputTransportFactory.GetTransport(client)
	outputTransport := p.outputTransportFactory.GetTransport(client)
	inputProtocol := p.inputProtocolFactory.GetProtocol(inputTransport)
	outputProtocol := p.outputProtocolFactory.GetProtocol(outputTransport)
	defer func() {
		if e := recover(); e != nil {
			log.Printf("panic in processor: %s: %s", e, debug.Stack())
		}
	}()
	if inputTransport != nil {
		defer inputTransport.Close()
	}
	if outputTransport != nil {
		defer outputTransport.Close()
	}
	for {
		ok, err := processor.Process(inputProtocol, outputProtocol)
		if err, ok := err.(TTransportException); ok && err.TypeId() == END_OF_FILE {
			return nil
		} else if err != nil {
			log.Printf("error processing request: %s", err)
			return err
		}
		if !ok {
			break
		}
	}
	return nil
}

func (p *TSimpleServer) ListenerFD() (uintptr, error) {
	l, ok := p.serverTransport.(*TServerSocket)
	if ok {
		file, err := l.ListenerTcp().File()
		if err != nil {
			return 0, err
		}
		return file.Fd(), nil
	}

	return 0, nil
}

func (p *TSimpleServer) Wait() {
	p.cm.Wait()
}

var WaitTimeoutError = errors.New("timeout")

func (p *TSimpleServer) WaitTimeoutError() error {
	return WaitTimeoutError
}

func (p *TSimpleServer) WaitWithTimeout(duration time.Duration) error {
	timeout := time.NewTimer(duration)
	wait := make(chan struct{})
	go func() {
		p.Wait()
		wait <- struct{}{}
	}()

	select {
	case <-timeout.C:
		return WaitTimeoutError
	case <-wait:
		return nil
	}
}

func (p *TSimpleServer) StopAccept() {
	// Accept will instantly return a timeout error
	p.serverTransport.(*TServerSocket).ListenerTcp().SetDeadline(time.Now())
}

func (p *TSimpleServer) ConnectionsCounter() int {
	return p.cm.Counter
}

type ConnectionManager struct {
	*sync.WaitGroup
	Counter int
}

func NewConnectionManager() *ConnectionManager {
	cm := &ConnectionManager{}
	cm.WaitGroup = &sync.WaitGroup{}
	return cm
}

func (cm *ConnectionManager) Add(delta int) {
	cm.Counter += delta
	cm.WaitGroup.Add(delta)
}

func (cm *ConnectionManager) Done() {
	cm.Counter--
	cm.WaitGroup.Done()
}
