package minecraft

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/aimjel/minecraft/packet"
	"github.com/aimjel/minecraft/player"
)

type ListenConfig struct {

	// Status handles the information showed to the client on the server list
	// which includes description, favicon, online/max players and protocol version and name
	Status *Status

	// OnlineMode enables server side encryption.
	// cracked accounts will not be able to connect when online mode is true.
	OnlineMode bool

	// CompressionThreshold compresses packets when they exceed n bytes.
	//-1 disables compression
	// 0 compresses everything
	CompressionThreshold int32

	Messages *Messages

	//todo add more config fields
}

func (lc *ListenConfig) Listen(address string) (*Listener, error) {
	if lc.Messages == nil {
		lc.Messages = &DefaultMessages
	}
	addr, err := net.ResolveTCPAddr("tcp4", address)
	if err != nil {
		return nil, err
	}

	ln, err := net.ListenTCP("tcp4", addr)
	if err != nil {
		return nil, err
	}

	var key *rsa.PrivateKey
	if lc.OnlineMode {
		key, err = rsa.GenerateKey(rand.Reader, 1024)
		if err != nil {
			return nil, err
		}
	}

	l := &Listener{
		cfg:   *lc,
		tcpLn: ln,
		key:   key,

		await: make(chan *Conn, 4),
	}

	//starts listening for incoming connections
	go l.listen()

	return l, nil
}

type Listener struct {
	cfg ListenConfig

	tcpLn *net.TCPListener

	key *rsa.PrivateKey

	err error

	await chan *Conn
}

func (l *Listener) listen() {
	for {
		c, err := l.tcpLn.AcceptTCP()
		if err != nil {
			l.err = err
			close(l.await)
			return
		}

		go l.handle(c)
	}
}

// handle new connections
func (l *Listener) handle(conn *net.TCPConn) {
	c := newConn(conn)

	var pk packet.Handshake
	if err := c.DecodePacket(&pk); err != nil {
		c.Close(err)
		return
	}

	switch pk.NextState {

	case 0x01: //status
		if err := l.handleStatus(c); err != nil && l.cfg.Status != nil {
			c.Close(fmt.Errorf("%v while handling status", err))
		}

	case 0x02:
		if pk.ProtocolVersion > int32(l.cfg.Status.s.Version.Protocol) {
			c.SendPacket(&packet.DisconnectLogin{
				Reason: l.cfg.Messages.ProtocolTooNew,
			})
		} else if pk.ProtocolVersion < int32(l.cfg.Status.s.Version.Protocol) {
			c.SendPacket(&packet.DisconnectLogin{
				Reason: l.cfg.Messages.ProtocolTooOld,
			})
		}
		if err := l.handleLogin(c); err != nil {
			c.Close(fmt.Errorf("%v while handling login", err))
		} else {
			if x := l.cfg.CompressionThreshold; x != -1 {
				c.enableCompression(x)
			}

			if c.SendPacket(&packet.LoginSuccess{Info: *c.Info}) != nil {
				c.Close(fmt.Errorf("%v while sending login success packet in login", err))
			} else {
				c.pool = &basicPool{}
				l.await <- c
				return //return so it doesn't close the connection
			}
		}
	}

	c.Close(nil)
}

func (l *Listener) handleStatus(c *Conn) error {
	var rq packet.Request
	if err := c.DecodePacket(&rq); err != nil {
		return err
	}

	if err := c.SendPacket(&packet.Response{JSON: l.cfg.Status.json()}); err != nil {
		return fmt.Errorf("%v writing response packet", err)
	}

	var pg packet.Ping
	if err := c.DecodePacket(&pg); err != nil {
		return fmt.Errorf("%v decoding ping packet", err)
	}

	return c.SendPacket(&packet.Pong{Payload: pg.Payload})
}

func (l *Listener) handleLogin(c *Conn) error {
	var ls packet.LoginStart
	if err := c.DecodePacket(&ls); err != nil {
		return err
	}

	if l.key == nil {
		var uuid [16]byte
		newUUIDv3(ls.Name, uuid[:])
		c.Info = &player.Info{UUID: uuid, Name: ls.Name}
		return nil
	}

	key, err := x509.MarshalPKIXPublicKey(&l.key.PublicKey)
	if err != nil {
		return err
	}

	token := make([]byte, 8)
	_, _ = rand.Read(token)

	if err = c.SendPacket(&packet.EncryptionRequest{PublicKey: key, VerifyToken: token}); err != nil {
		return err
	}

	var encryptResp packet.EncryptionResponse
	if err = c.DecodePacket(&encryptResp); err != nil {
		return err
	}

	var (
		sharedSecret, verifyToken []byte
	)
	if sharedSecret, err = l.key.Decrypt(nil, encryptResp.SharedSecret, nil); err != nil {
		return err
	}

	if verifyToken, err = l.key.Decrypt(nil, encryptResp.VerifyToken, nil); err != nil {
		return err
	}

	if !bytes.Equal(verifyToken, token) {
		return fmt.Errorf("failed to verify token")
	}

	if err := c.enableEncryption(sharedSecret); err != nil {
		return err
	}

	loginHash, err := l.generateHash(sharedSecret)
	if err != nil {
		return err
	}

	r, err := http.DefaultClient.Get("https://sessionserver.mojang.com/session/minecraft/hasJoined?username=" + ls.Name + "&serverId=" + loginHash)
	if err != nil {
		return fmt.Errorf("%v getting player data", err)
	}

	var data struct {
		Id         string `json:"id"`
		Name       string `json:"name"`
		Properties []struct {
			Name      string `json:"name"`
			Value     string `json:"value"`
			Signature string `json:"signature"`
		} `json:"properties"`
	}

	if err = json.NewDecoder(r.Body).Decode(&data); err != nil && err != io.EOF {
		return err
	}
	_ = r.Body.Close()

	uuid, err := hex.DecodeString(data.Id)
	if err != nil {
		return err
	}

	c.Info = &player.Info{Name: data.Name, Properties: []struct {
		Name      string
		Value     string
		Signature string
	}(data.Properties)}

	if n := copy(c.Info.UUID[:], uuid); n != 16 {
		return fmt.Errorf("expected 16 bytes from uuid got %v", n)
	}
	return nil
}

func (l *Listener) Accept() (*Conn, error) {
	c, ok := <-l.await
	if !ok {
		if l.err != nil {
			return nil, l.err
		}

		return nil, net.ErrClosed
	}

	return c, nil
}

// generateHash generates the login hash sent in the HTTP Get to retrieve uuid, name, textures
func (l *Listener) generateHash(sharedSecret []byte) (string, error) {
	h := sha1.New()
	h.Write(sharedSecret)

	key, err := x509.MarshalPKIXPublicKey(&l.key.PublicKey)
	if err != nil {
		return "", err
	}

	h.Write(key)
	loginHash := h.Sum(nil)

	neg := loginHash[0] >= 128
	if neg {
		twosComplement(loginHash)
	}

	hs := strings.TrimLeft(hex.EncodeToString(loginHash), "0")
	if neg {
		hs = "-" + hs
	}

	return hs, nil
}

func twosComplement(p []byte) {
	//invert all the bites
	for k, v := range p {
		p[k] = ^v
	}

	// Add 1
	carry := byte(1)
	for i := len(p) - 1; i >= 0; i-- {
		p[i] += carry
		carry = p[i] >> 8
		p[i] &= 0xFF
		if carry == 0 {
			break
		}
	}
}

func newUUIDv3(name string, out []byte) {
	h := md5.New()
	h.Write([]byte("OfflinePlayer:" + name))
	id := h.Sum(nil)

	id[6] = (id[6] & 0x0f) | uint8((3&0xf)<<4)
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 variant

	copy(out, id)
}
