package fluentd_forwarder

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io/ioutil"
	"log"
	"math/rand"
	"net"
	"os"
	"regexp"
	"time"

	bl "../../basiclogger"
	"gopkg.in/vmihailenco/msgpack.v2"
)

const module = "fluentd_forwarder"

type Fluentd_forwarder struct {
	tag                           string
	c                             chan *bl.Message
	conn                          net.Conn
	SSLCertificate, SSLKey, SSLCA string
	tlsConfig                     tls.Config
	tlsEnabled                    bool
	host                          string
	hosts                         []string
	timeout                       time.Duration
}

var hostname string
var hostport_re, _ = regexp.Compile("^(.+):([0-9]+)$")

func init() {
	hostname, _ = os.Hostname()
	rand.Seed(time.Now().UnixNano())
	bl.RegisterOutput(module, Init)
}

func Init(conf map[string]interface{}) bl.Output {
	hosts := bl.GArrString("hosts", conf)
	if len(hosts) == 0 {
		log.Fatalf("[ERROR] [%s] There is no valid hosts", module)
	} else {
		timeout := int64(bl.GInt("timeout", conf))
		if timeout <= 0 {
			log.Fatalf("[ERROR] [%s] You must specify right timeout (%v)", module, timeout)
		} else {
			SSLCertificate := bl.GString("ssl_cert", conf)
			SSLKey := bl.GString("ssl_key", conf)
			SSLCA := bl.GString("ssl_ca", conf)
			tag := bl.GString("tag", conf)
			res := Fluentd_forwarder{
				tag:            tag,
				c:              make(chan *bl.Message),
				conn:           nil,
				hosts:          hosts,
				SSLCertificate: SSLCertificate,
				SSLKey:         SSLKey,
				SSLCA:          SSLCA,
				timeout:        time.Second * time.Duration(timeout)}
			res.loadCerts()
			return &res
		}
	}
	return nil
}

func (v *Fluentd_forwarder) Tag() string {
	return v.tag
}

func (v *Fluentd_forwarder) Send(message *bl.Message) error {
	var err error
	if _, err = time.Parse(bl.TIMEFORMAT, bl.GString("@timestamp", message.Data)); err != nil {
		fmt.Printf("[WARN] [%s] Bogus @timestamp field: %v", v.tag, message.Data["@timestamp"])
		message.Data["@timestamp"] = message.Time.Format(bl.TIMEFORMAT)
	}
	now := time.Now().UnixNano()
	for {
		if v.conn == nil {
			if v.connect() != nil {
				time.Sleep(time.Second)
				continue
			}
		}
		if message.Data["host"] == nil || message.Data["host"] == "" {
			message.Data["host"] = hostname
		}
		val := []interface{}{v.tag, now, message.Data}
		m, err := msgpack.Marshal(val)
		if err != nil {
			fmt.Printf("[WARN] [%s] Bogus message: %v", v.tag, message.Data)
			break
		}
		v.conn.SetDeadline(time.Now().Add(v.timeout))
		_, err = v.conn.Write(m)
		if err != nil {
			log.Printf("[WARN] [%s] Socket write error %v", v.tag, err)
			v.conn.Close()
			v.conn = nil
			time.Sleep(time.Second)
			continue
		}
		break
	}
	return err
}

func (v *Fluentd_forwarder) connect() error {
	hostport := v.hosts[rand.Int()%len(v.hosts)]
	submatch := hostport_re.FindSubmatch([]byte(hostport))
	host := string(submatch[1])
	port := string(submatch[2])
	ips, err := net.LookupIP(host)

	if err != nil {
		log.Printf("[WARN] [%s] DNS lookup failure \"%s\": %s", v.tag, host, err)
		return err
	}

	ip := ips[rand.Int()%len(ips)]
	var addressport string

	address := ip.String()
	if len(ip) == net.IPv4len {
		addressport = fmt.Sprintf("%s:%s", address, port)
	} else if len(ip) == net.IPv6len {
		addressport = fmt.Sprintf("[%s]:%s", address, port)
	}

	log.Printf("[DEBUG] [%s] Connecting to %s (%s) \n", v.tag, addressport, host)

	conn, err := net.DialTimeout("tcp", addressport, v.timeout)
	if err != nil {
		log.Printf("[WARN] [%s] Failure connecting to %s: %s\n", v.tag, address, err)
		return err
	} else {
		v.host = host
		v.conn = conn
	}
	if v.tlsEnabled {
		v.tlsConfig.ServerName = host
		connTLS := tls.Client(conn, &v.tlsConfig)
		connTLS.SetDeadline(time.Now().Add(v.timeout))
		err = connTLS.Handshake()
		if err != nil {
			log.Printf("[WARN] [%s] Failed to tls handshake with %s %s\n", v.tag, address, err)
			connTLS.Close()
			return err
		} else {
			log.Printf("[DEBUG] [%s] Successful TLS handshake %s\n", v.tag, address)
			v.conn = connTLS
		}
	}
	log.Printf("[INFO] [%s] Connected to %s\n", v.tag, address)
	return nil
}

func (v *Fluentd_forwarder) loadCerts() {
	v.tlsEnabled = false
	v.tlsConfig.MinVersion = tls.VersionTLS10
	if len(v.SSLCertificate) > 0 && len(v.SSLKey) > 0 {
		log.Printf("[INFO] [%s] Loading client ssl certificate and key from \"%s\" and \"%s\"\n", v.tag,
			v.SSLCertificate, v.SSLKey)
		cert, err := tls.LoadX509KeyPair(v.SSLCertificate, v.SSLKey)
		if err != nil {
			log.Fatalf("[ERROR] [%s] Failed loading client ssl certificate: %s\n", v.tag, err)
		}
		v.tlsConfig.Certificates = []tls.Certificate{cert}
		v.tlsEnabled = true
	}

	if len(v.SSLCA) > 0 {
		log.Printf("[INFO] [%s] Loading CA certificate from file: %s\n", v.tag, v.SSLCA)
		v.tlsConfig.RootCAs = x509.NewCertPool()

		pemdata, err := ioutil.ReadFile(v.SSLCA)
		if err != nil {
			log.Fatalf("[ERROR] [%s] Failure reading CA certificate: %s\n", v.tag, err)
		}

		block, _ := pem.Decode(pemdata)
		if block == nil {
			log.Fatalf("[ERROR] [%s] Failed to decode PEM data of CA certificate from \"%s\"\n", v.tag, v.SSLCA)
		}
		if block.Type != "CERTIFICATE" {
			log.Fatalf("[ERROR] [%s] This is not a certificate file: %s\n", v.tag, v.SSLCA)
		}

		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			log.Fatalf("[ERROR] [%s] Failed to parse CA certificate: %s\n", v.tag, v.SSLCA)
		}
		v.tlsConfig.RootCAs.AddCert(cert)
		v.tlsEnabled = true
	}
}
