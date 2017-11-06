package kafka

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
	"github.com/gliderlabs/logspout/router"
	"gopkg.in/Shopify/sarama.v1"
)


const (
	NO_MESSAGE_PROVIDED     = "no message"
	LOGTYPE_APPLICATIONLOG  = "applog"
	LOGTYPE_ACCESSLOG       = "accesslog"
)

func init() {
	router.AdapterFactories.Register(NewKafkaAdapter, "kafka")
}

type KafkaAdapter struct {
	route    *router.Route
	brokers  []string
	topic    string
	producer sarama.AsyncProducer
	docker_host   string
	use_v0        bool
	logstash_type string
	dedot_labels  bool
	msg_counter   int
}

type DockerFields struct {
	Name       string            `json:"name"`
	CID        string            `json:"cid"`
	Image      string            `json:"image"`
	ImageTag   string            `json:"image_tag,omitempty"`
	Source     string            `json:"source"`
	DockerHost string            `json:"docker_host,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
}

type LogstashFields struct {
	Docker DockerFields `json:"docker"`
}

type LogstashMessageV0 struct {
	Type       string         `json:"@type,omitempty"`
	Timestamp  string         `json:"@timestamp"`
	Sourcehost string         `json:"@source_host"`
	Message    string         `json:"@message"`
	Fields     LogstashFields `json:"@fields"`
}

type LogstashMessageV1 struct {
	Type       string       `json:"@type,omitempty"`
	Timestamp  string       `json:"@timestamp"`
	Sourcehost string       `json:"host"`
	Message    string       `json:"message"`
	Fields     DockerFields `json:"docker"`
	Logtype    string       `json:"logtype,omitempty"`
	// Only one of the following 3 is initialized and used, depending on the incoming json:logtype
	LogtypeAccessfields map[string]interface{} `json:"accesslog,omitempty"`
	LogtypeAppfields    map[string]interface{} `json:"applog,omitempty"`
	LogtypeEventfields  map[string]interface{} `json:"event,omitempty"`
}

func NewKafkaAdapter(route *router.Route) (router.LogAdapter, error) {
	brokers := readBrokers(route.Address)
	if len(brokers) == 0 {
		return nil, errorf("The Kafka broker host:port is missing. Did you specify it as a route address?")
	}

	topic := readTopic(route.Address, route.Options)
	if topic == "" {
		return nil, errorf("The Kafka topic is missing. Did you specify it as a route option?")
	}

	var err error

	if os.Getenv("DEBUG") != "" {
		log.Printf("Starting Kafka producer for address: %s, topic: %s.\n", brokers, topic)
	}

	var retries int
	retries, err = strconv.Atoi(os.Getenv("KAFKA_CONNECT_RETRIES"))
	if err != nil {
		retries = 3
	}
	var producer sarama.AsyncProducer
	for i := 0; i < retries; i++ {
		producer, err = sarama.NewAsyncProducer(brokers, newConfig())
		if err != nil {
			if os.Getenv("DEBUG") != "" {
				log.Println("Couldn't create Kafka producer. Retrying...", err)
			}
			if i == retries-1 {
				return nil, errorf("Couldn't create Kafka producer. %v", err)
			}
		} else {
			time.Sleep(1 * time.Second)
		}
	}

	return &KafkaAdapter{
		route:    route,
		brokers:  brokers,
		topic:    topic,
		producer: producer,
	}, nil
}

func (a *KafkaAdapter) Stream(logstream chan *router.Message) {
	defer a.producer.Close()


	for rm := range logstream {

		a.msg_counter += 1

		message, err := createLogstashMessage(rm, a.topic, a.docker_host, a.use_v0, a.logstash_type, a.dedot_labels)
		if err != nil {
			log.Println("kafka:", err)
			a.route.Close()
			break
		}

		a.producer.Input() <- message
	}
}



func newConfig() *sarama.Config {
	config := sarama.NewConfig()
	config.ClientID = "logspout"
	config.Producer.Return.Errors = false
	config.Producer.Return.Successes = false
	config.Producer.Flush.Frequency = 1 * time.Second
	config.Producer.RequiredAcks = sarama.WaitForLocal

	if opt := os.Getenv("KAFKA_COMPRESSION_CODEC"); opt != "" {
		switch opt {
		case "gzip":
			config.Producer.Compression = sarama.CompressionGZIP
		case "snappy":
			config.Producer.Compression = sarama.CompressionSnappy
		}
	}

	return config
}

func splitImage(image_tag string) (image string, tag string) {
	colon := strings.LastIndex(image_tag, ":")
	sep := strings.LastIndex(image_tag, "/")
	if colon > -1 && sep < colon {
		image = image_tag[0:colon]
		tag = image_tag[colon+1:]
	} else {
		image = image_tag
	}
	return
}

func dedotLabels(labels map[string]string) map[string]string {
	for key, _ := range labels {
		if strings.Contains(key, ".") {
			dedotted_label := strings.Replace(key, ".", "_", -1)
			labels[dedotted_label] = labels[key]
			delete(labels, key)
		}
	}

	return labels
}

func validJsonMessage(s string) bool {

	if !strings.HasPrefix(s, "{") || !strings.HasSuffix(s, "}") {
		return false
	}
	return true
}

func createLogstashMessage(m *router.Message, topic string, docker_host string, use_v0 bool, logstash_type string, dedot_labels bool) (*sarama.ProducerMessage, error) {
	image, image_tag := splitImage(m.Container.Config.Image)
	cid := m.Container.ID[0:12]
	name := m.Container.Name[1:]
	timestamp := m.Time.UTC().Format(time.RFC3339Nano)

		msg := LogstashMessageV1{}

		msg.Type = logstash_type
		msg.Timestamp = timestamp
		msg.Sourcehost = m.Container.Config.Hostname
		msg.Fields.CID = cid
		msg.Fields.Name = name
		msg.Fields.Image = image
		msg.Fields.ImageTag = image_tag
		msg.Fields.Source = m.Source
		msg.Fields.DockerHost = docker_host

		// see https://github.com/rtoma/logspout-redis-logstash/issues/11
		if dedot_labels {
			msg.Fields.Labels = dedotLabels(m.Container.Config.Labels)
		} else {
			msg.Fields.Labels = m.Container.Config.Labels
		}

		// Check if the message to log itself is json
		if validJsonMessage(strings.TrimSpace(m.Data)) {
			// So it is, include it in the LogstashmessageV1
			err := msg.UnmarshalDynamicJSON([]byte(m.Data))
			if err != nil {
				// Can't unmarshall the json (invalid?), put it in message
				msg.Message = m.Data
			} else if msg.Message == "" {
				msg.Message = NO_MESSAGE_PROVIDED
			}
		} else {
			// Regular logging (no json)
			msg.Message = m.Data
		}

		var encoder sarama.Encoder
		jsonMessage,_ := json.Marshal(msg)
		encoder = sarama.ByteEncoder(jsonMessage)

		return &sarama.ProducerMessage{
		Topic: topic,
		Value: encoder,
		}, nil
}

func (d *LogstashMessageV1) UnmarshalDynamicJSON(data []byte) error {
	var dynMap map[string]interface{}

	if d == nil {
		return errors.New("RawString: UnmarshalJSON on nil pointer")
	}

	if err := json.Unmarshal(data, &dynMap); err != nil {
		return err
	}

	// Take logtype of the hash, but only if it is a valid logtype
	if _, ok := dynMap["logtype"].(string); ok {
		if dynMap["logtype"].(string) == LOGTYPE_APPLICATIONLOG || dynMap["logtype"].(string) == LOGTYPE_ACCESSLOG {
			d.Logtype = dynMap["logtype"].(string)
			delete(dynMap, "logtype")
		}
	}
	// Take message out of the hash
	if _, ok := dynMap["message"]; ok {
		d.Message = dynMap["message"].(string)
		delete(dynMap, "message")
	}

	// Only initialize the "used" hash in struct
	if d.Logtype == LOGTYPE_APPLICATIONLOG {
		d.LogtypeAppfields = make(map[string]interface{}, 0)
	} else if d.Logtype == LOGTYPE_ACCESSLOG {
		d.LogtypeAccessfields = make(map[string]interface{}, 0)
	} else {
		d.LogtypeEventfields = make(map[string]interface{}, 0)
	}

	// Fill the right hash based on logtype
	for key, val := range dynMap {
		if d.Logtype == LOGTYPE_APPLICATIONLOG {
			d.LogtypeAppfields[key] = val
		} else if d.Logtype == LOGTYPE_ACCESSLOG {
			d.LogtypeAccessfields[key] = val
		} else {
			d.LogtypeEventfields[key] = val
		}
	}

	return nil
}

func readBrokers(address string) []string {
	if strings.Contains(address, "/") {
		slash := strings.Index(address, "/")
		address = address[:slash]
	}

	return strings.Split(address, ",")
}

func readTopic(address string, options map[string]string) string {
	var topic string
	if !strings.Contains(address, "/") {
		topic = options["topic"]
	} else {
		slash := strings.Index(address, "/")
		topic = address[slash+1:]
	}

	return topic
}

func errorf(format string, a ...interface{}) (err error) {
	err = fmt.Errorf(format, a...)
	if os.Getenv("DEBUG") != "" {
		fmt.Println(err.Error())
	}
	return
}
