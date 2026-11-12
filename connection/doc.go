// Package connection opens the platform's MySQL, Redis, RabbitMQ and MQTT
// connections from the standard environment variables.
//
//	db, err := connection.OpenDatabaseFromEnv()
//	reports, err := connection.OpenReportsDatabaseFromEnv()
//	cache, err := connection.OpenRedisFromEnv()
//	global, err := connection.OpenGlobalRedisFromEnv()
//	mq, err := connection.DialRabbitMQFromEnv()
//	client, err := connection.ConnectMQTTFromEnv()
//
// Each constructor has a WithContext variant, and a form that takes an explicit
// config. Every constructor validates its settings and verifies the connection
// before returning, so a misconfigured dependency fails at startup.
package connection
