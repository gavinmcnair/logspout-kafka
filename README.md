# logspout-kafka

A [Logspout](https://github.com/gliderlabs/logspout) adapter for writing Docker container logs to [Kafka](https://github.com/apache/kafka) topics in the logstash JSON format.

This was a quick hack with the intention of being used with the kafka-elastic connect adapter.

If it is of use to you please contribute (issues/updates/tests/etc)

## usage

With *container-logs* as the Kafka topic for Docker container logs, we can direct all messages to Kafka using the **logspout** [Route API](https://github.com/gliderlabs/logspout/tree/master/routesapi):

```
curl http://container-host:8000/routes -d '{
  "adapter": "kafka",
  "filter_sources": ["stdout" ,"stderr"],
  "address": "kafka-broker1:9092,kafka-broker2:9092/container-logs"
}'
```

## logstash output

This is a modified version of the adapter based on the existing Redis and Kafka adapters. Its designed to output logstash style messages.

*Example Output*

```json
{
	"@timestamp": "2017-11-06T23:39:38.643921569Z",
	"host": "d23b91da8640",
	"message": "10.11.12.13 - - [06/Nov/2017:23:39:38 +0000] \"GET /v2/ HTTP/1.1\" 200 2 \"\" \"spray-can/1.3.4\"",
	"docker": {
		"name": "mesos-d37bc567-c206-4697-b703-08fe74855621-S7.555e8afc-1eaa-4372-896f-378b4abb7dcc",
		"cid": "d23b91da8640",
		"image": "registry",
		"image_tag": "2",
		"source": "stdout",
		"labels": {
			"MESOS_TASK_ID": "docker-registry-balanced.cb184fd5-8690-11e7-af49-520b0a5ccc33"
		}
	}
}
```



## route configuration

If you've mounted a volume to `/mnt/routes`, then consider pre-populating your routes. The following script configures a route to send standard messages from a "cat" container to one Kafka topic, and a route to send standard/error messages from a "dog" container to another topic.

```
cat > /logspout/routes/cat.json <<CAT
{
  "id": "cat",
  "adapter": "kafka",
  "filter_name": "cat_*",
  "filter_sources": ["stdout"],
  "address": "kafka-broker1:9092,kafka-broker2:9092/cat-logs"
}
CAT

cat > /logspout/routes/dog.json <<DOG
{
  "id": "dog",
  "adapter": "kafka",
  "filter_name": "dog_*",
  "filter_sources": ["stdout", "stderr"],
  "address": "kafka-broker1:9092,kafka-broker2:9092/dog-logs"
}
DOG

docker run --name logspout \
  -p "8000:8000" \
  --volume /logspout/routes:/mnt/routes \
  --volume /var/run/docker.sock:/tmp/docker.sock \
  gettyimages/example-logspout

```

The routes can be updated on a running container by using the **logspout** [Route API](https://github.com/gliderlabs/logspout/tree/master/routesapi) and specifying the route `id` "cat" or "dog".

## build

**logspout-kafka** is a custom **logspout** module. To use it, create an empty `Dockerfile` based on `gliderlabs/logspout` and include this **logspout-kafka** module in a new `modules.go` file.

The following example creates an almost-minimal **logspout** image capable of writing Docker container logs to Kafka topics:

```
cat > ./Dockerfile.example <<DOCKERFILE
FROM gliderlabs/logspout:master

ENV KAFKA_COMPRESSION_CODEC snappy
DOCKERFILE

cat > ./modules.go <<MODULES
package main
import (
  _ "github.com/gliderlabs/logspout/httpstream"
  _ "github.com/gliderlabs/logspout/routesapi"
  _ "github.com/gettyimages/logspout-kafka"
)
MODULES

docker build -t gettyimages/example-logspout -f Dockerfile.example .
```

More info about building custom modules is available at the **logspout** project: [Custom Logspout Modules](https://github.com/gliderlabs/logspout/blob/master/custom/README.md)
