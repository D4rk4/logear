package in_logear_forwarder

import (
	"bytes"
	"compress/zlib"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/pem"
	"io"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"regexp"
	"time"

	bl "../../basiclogger"
	"gopkg.in/vmihailenco/msgpack.v2"
)

const module = "in_logear_forwarder"

type In_logear_forwarder struct {
	tag          string
	messageQueue chan *bl.Message
	tlsConfig    tls.Config
	bind         string
	timeout      time.Duration
}

var hostport_re, _ = regexp.Compile("^(.+):([0-9]+)$")

func init() {
	rand.Seed(time.Now().UnixNano())
	bl.RegisterInput(module, Init)
}

func Init(messageQueue chan *bl.Message, conf map[string]interface{}) bl.Input {
	var tlsConfig tls.Config
	tag := bl.GString("tag", conf)
	bind := bl.GString("bind", conf)
	timeout := int64(bl.GInt("timeout", conf))
	if timeout <= 0 {
		log.Fatalf("[ERROR] [%s] You must specify right timeout (%d)", module, timeout)
	}
	SSLCertificate := bl.GString("ssl_cert", conf)
	SSLKey := bl.GString("ssl_key", conf)
	SSLCA := bl.GString("ssl_ca", conf)
	if len(SSLCertificate) > 0 && len(SSLKey) > 0 {
		tlsConfig.MinVersion = tls.VersionTLS12
		log.Printf("[INFO] [%s] Loading server ssl certificate and key from \"%s\" and \"%s\"", tag,
			SSLCertificate, SSLKey)
		cert, err := tls.LoadX509KeyPair(SSLCertificate, SSLKey)
		if err != nil {
			log.Fatalf("[ERROR] [%s] Failed loading server ssl certificate: %s", tag, err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
		if len(SSLCA) > 0 {
			log.Printf("[INFO] [%s] Loading CA certificate from file: %s\n", tag, SSLCA)
			tlsConfig.ClientCAs = x509.NewCertPool()
			tlsConfig.ClientAuth = tls.RequireAndVerifyClientCert
			pemdata, err := ioutil.ReadFile(SSLCA)
			if err != nil {
				log.Fatalf("[ERROR] [%s] Failure reading CA certificate: %s\n", tag, err)
			}

			block, _ := pem.Decode(pemdata)
			if block == nil {
				log.Fatalf("[ERROR] [%s] Failed to decode PEM data of CA certificate from \"%s\"\n", tag, SSLCA)
			}
			if block.Type != "CERTIFICATE" {
				log.Fatalf("[ERROR] [%s] This is not a certificate file: %s\n", tag, SSLCA)
			}

			cacert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				log.Fatalf("[ERROR] [%s] Failed to parse CA certificate: %s\n", tag, SSLCA)
			}
			tlsConfig.ClientCAs.AddCert(cacert)
		}

		v := &In_logear_forwarder{tag: tag,
			messageQueue: messageQueue,
			tlsConfig:    tlsConfig,
			bind:         bind,
			timeout:      time.Second * time.Duration(timeout)}
		return v
	} else {
		log.Fatalf("[ERROR] [%s] You must specify ssl_cert and ssl_key", module)
	}
	return nil
}

func (v *In_logear_forwarder) Tag() string {
	return v.tag
}

func (v *In_logear_forwarder) Listener() {
	go v.listen()
}

func (v *In_logear_forwarder) listen() {
	listener, err := tls.Listen("tcp4", v.bind, &v.tlsConfig)
	if err != nil {
		log.Fatalf("[ERROR] [%s] Can't start listen \"%s\", error: %v", v.tag, v.bind, err)
	}
	defer listener.Close()
	log.Printf("[INFO] [%s] Waiting for connections", v.tag)
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("[WARN] [%s] Can't accept client %v", v.tag, err)
		}
		go v.worker(conn)
	}
}

func (v *In_logear_forwarder) worker(conn net.Conn) {

	log.Printf("[DEBUG] [%s] Accepted connection from %s", v.tag, conn.RemoteAddr().String())
	for {
		conn.SetReadDeadline(time.Now().Add(v.timeout))
		csize, err := v.readInt64(conn)
		if err != nil {
			log.Printf("[WARN] [%s] Can't read size of compressed payload, closing connection, error: %v", v.tag, err)
			conn.Close()
			return
		}
		size, err := v.readInt64(conn)
		if err != nil {
			log.Printf("[WARN] [%s] Can't read size of uncompressed payload, closing connection, error: %v", v.tag, err)
			conn.Close()
			return
		}
		log.Printf("[DEBUG] [%s] Waiting for %d bytes of payload", v.tag, csize)
		cpayload := make([]byte, int(csize))
		n, err := conn.Read(cpayload)
		if err != nil || int64(n) != csize {
			log.Printf("[WARN] [%s] Can't read compressed payload, closing connection, error: %v", v.tag, err)
			conn.Close()
			return
		}
		bpayload := bytes.NewReader(cpayload)
		zpayload, err := zlib.NewReader(bpayload)
		if err != nil {
			log.Printf("[WARN] [%s] Can't start zlib handler, closing connection, error: %v", v.tag, err)
			conn.Close()
			return
		}
		payload := make([]byte, size)
		n, err = zpayload.Read(payload)
		if err != nil || int64(n) != size {
			log.Printf("[WARN] [%s] Can't uncompress payload, closing connection, error: %v", v.tag, err)
			conn.Close()
			return
		}
		var data map[string]interface{}
		err = msgpack.Unmarshal(payload, &data)
		if err != nil {
			log.Printf("[WARN] [%s] Can't parse payload error: %v", v.tag, err)
			conn.Close()
			return
		}
		if _, ok := data["@timestamp"]; !ok {
			data["@timestamp"] = time.Now()
		}
		v.messageQueue <- &bl.Message{Time: time.Now(), Data: data}
	}
}

func (v *In_logear_forwarder) readInt64(r io.Reader) (int64, error) {
	var data int64
	err := binary.Read(r, binary.BigEndian, &data)
	if err != nil {
		return 0, err
	}
	return data, nil
}
