// Copyright 2020 The gVisor Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package gofer

import (
	"gvisor.dev/gvisor/pkg/abi/linux"
	"gvisor.dev/gvisor/pkg/context"
	"gvisor.dev/gvisor/pkg/log"
	"gvisor.dev/gvisor/pkg/p9"
	"gvisor.dev/gvisor/pkg/sentry/fsimpl/host"
	"gvisor.dev/gvisor/pkg/sentry/socket/unix/transport"
	"gvisor.dev/gvisor/pkg/syserr"
	"gvisor.dev/gvisor/pkg/waiter"
)

func (d *dentry) isSocket() bool {
	return d.fileType() == linux.S_IFSOCK
}

// endpoint is a Gofer-backed transport.BoundEndpoint.
//
// An endpoint's lifetime is the time between when filesystem.BoundEndpointAt()
// is called and either BoundEndpoint.BidirectionalConnect or
// BoundEndpoint.UnidirectionalConnect is called.
type endpoint struct {
	// dentry is the filesystem dentry which produced this endpoint.
	dentry *dentry

	// file is the p9 file that contains a single unopened fid.
	file p9.File

	// path is the sentry path where this endpoint is bound.
	path string
}

func sockTypeToP9(t linux.SockType) (p9.ConnectFlags, bool) {
	switch t {
	case linux.SOCK_STREAM:
		return p9.StreamSocket, true
	case linux.SOCK_SEQPACKET:
		return p9.SeqpacketSocket, true
	case linux.SOCK_DGRAM:
		return p9.DgramSocket, true
	}
	return 0, false
}

// BidirectionalConnect implements ConnectableEndpoint.BidirectionalConnect.
func (e *endpoint) BidirectionalConnect(ctx context.Context, ce transport.ConnectingEndpoint, returnConnect func(transport.Receiver, transport.ConnectedEndpoint)) *syserr.Error {
	cf, ok := sockTypeToP9(ce.Type())
	if !ok {
		return syserr.ErrConnectionRefused
	}

	// No lock ordering required as only the ConnectingEndpoint has a mutex.
	ce.Lock()

	// Check connecting state.
	if ce.Connected() {
		ce.Unlock()
		return syserr.ErrAlreadyConnected
	}
	if ce.Listening() {
		ce.Unlock()
		return syserr.ErrInvalidEndpointState
	}

	hostFile, err := e.file.Connect(cf)
	if err != nil {
		ce.Unlock()
		return syserr.ErrConnectionRefused
	}

	c, serr := host.NewConnectedEndpoint(ctx, hostFile, ce.WaiterQueue(), e.path, false /* saveable */)
	if serr != nil {
		ce.Unlock()
		log.Warningf("Gofer returned invalid host socket for BidirectionalConnect; file %+v flags %+v: %v", e.file, cf, serr)
		return serr
	}

	returnConnect(c, c)
	ce.Unlock()
	c.Init()

	return nil
}

// UnidirectionalConnect implements
// transport.BoundEndpoint.UnidirectionalConnect.
func (e *endpoint) UnidirectionalConnect(ctx context.Context) (transport.ConnectedEndpoint, *syserr.Error) {
	hostFile, err := e.file.Connect(p9.DgramSocket)
	if err != nil {
		return nil, syserr.ErrConnectionRefused
	}

	c, serr := host.NewConnectedEndpoint(ctx, hostFile, &waiter.Queue{}, e.path, false /* saveable */)
	if serr != nil {
		log.Warningf("Gofer returned invalid host socket for UnidirectionalConnect; file %+v: %v", e.file, serr)
		return nil, serr
	}
	c.Init()

	// We don't need the receiver.
	c.CloseRecv()
	c.Release()

	return c, nil
}

// Release implements transport.BoundEndpoint.Release.
func (e *endpoint) Release() {
	e.dentry.DecRef()
}

// Passcred implements transport.BoundEndpoint.Passcred.
func (e *endpoint) Passcred() bool {
	return false
}
