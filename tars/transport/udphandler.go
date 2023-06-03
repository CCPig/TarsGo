package transport

import (
	"context"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/TarsCloud/TarsGo/tars/protocol/res/basef"
	"github.com/TarsCloud/TarsGo/tars/util/current"
	"github.com/TarsCloud/TarsGo/tars/util/grace"
)

type udpHandler struct {
	config *TarsServerConf
	server *TarsServer

	conn *net.UDPConn
}

func (u *udpHandler) Listen() (err error) {
	cfg := u.config
	u.conn, err = grace.CreateUDPConn(cfg.Address)
	if err != nil {
		return err
	}
	TLOG.Info("UDP listen", u.conn.LocalAddr())
	return nil
}

func (u *udpHandler) getConnContext(udpAddr *net.UDPAddr) context.Context {
	ctx := current.ContextWithTarsCurrent(context.Background())
	current.SetClientIPWithContext(ctx, udpAddr.IP.String())
	current.SetClientPortWithContext(ctx, strconv.Itoa(udpAddr.Port))
	current.SetRecvPkgTsFromContext(ctx, time.Now().UnixNano()/1e6)
	current.SetRawConnWithContext(ctx, u.conn, udpAddr)
	return ctx
}

func (u *udpHandler) Handle() error {
	atomic.AddInt32(&u.server.numConn, 1)
	// wait invoke done
	defer func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for atomic.LoadInt32(&u.server.numInvoke) > 0 {
			<-tick.C
		}
		atomic.AddInt32(&u.server.numConn, -1)
	}()
	buffer := make([]byte, 65535)
	for {
		if atomic.LoadInt32(&u.server.isClosed) == 1 {
			return nil
		}
		n, udpAddr, err := u.conn.ReadFromUDP(buffer)
		if err != nil {
			if atomic.LoadInt32(&u.server.isClosed) == 1 {
				return nil
			}
			if isNoDataError(err) {
				continue
			}
			TLOG.Errorf("Close connection %s: %v", u.config.Address, err)
			return err // TODO: check if necessary
		}
		pkg := make([]byte, n)
		copy(pkg, buffer[0:n])
		go func() {
			atomic.AddInt32(&u.server.numInvoke, 1)
			defer atomic.AddInt32(&u.server.numInvoke, -1)
			ctx := u.getConnContext(udpAddr)
			rsp := u.server.invoke(ctx, pkg) // no need to check package

			cPacketType, ok := current.GetPacketTypeFromContext(ctx)
			if !ok {
				TLOG.Error("Failed to GetPacketTypeFromContext")
			}

			if cPacketType == basef.TARSONEWAY {
				return
			}

			if _, err := u.conn.WriteToUDP(rsp, udpAddr); err != nil {
				TLOG.Errorf("send pkg to %v failed %v", udpAddr, err)
			}
		}()
	}
}

func (u *udpHandler) OnShutdown() {
}

func (u *udpHandler) CloseIdles(_ int64) bool {
	if u.server.numInvoke == 0 {
		u.conn.Close()
		return true
	}
	return false
}
