//go:generate ../../../tools/readme_config_includer/generator
package kafka_consumer_vdojot

import (
	"context"
	_ "embed"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Shopify/sarama"

	"github.com/influxdata/telegraf"
	"github.com/influxdata/telegraf/config"
	"github.com/influxdata/telegraf/internal"
	"github.com/influxdata/telegraf/plugins/common/kafka"
	"github.com/influxdata/telegraf/plugins/inputs"
	"github.com/influxdata/telegraf/plugins/parsers"
)

//go:embed sample.conf
var sampleConfig string

const (
	defaultMaxUndeliveredMessages = 1000
	defaultMaxProcessingTime      = config.Duration(100 * time.Millisecond)
	defaultConsumerGroup          = "telegraf_metrics_consumers"
	reconnectDelay                = 5 * time.Second
)

type empty struct{}
type semaphore chan empty

type KafkaConsumerDojot struct {
	Brokers                []string        `toml:"brokers"`
	ConsumerGroup          string          `toml:"consumer_group"`
	MaxMessageLen          int             `toml:"max_message_len"`
	MaxUndeliveredMessages int             `toml:"max_undelivered_messages"`
	MaxProcessingTime      config.Duration `toml:"max_processing_time"`
	Offset                 string          `toml:"offset"`
	BalanceStrategy        string          `toml:"balance_strategy"`
	Topics                 []string        `toml:"topics"`
	TopicTag               string          `toml:"topic_tag"`
	ConsumerFetchDefault   config.Size     `toml:"consumer_fetch_default"`
	TopicRefreshInterval   string          `toml:"topics_refresh_interval"`

	kafka.ReadConfig

	Log telegraf.Logger `toml:"-"`

	ConsumerCreator ConsumerGroupCreator `toml:"-"`
	ClientCreator   StreamClientCreator
	consumer        ConsumerGroup
	client          StreamClient
	selectedTopics  []string
	topicRegex      *regexp.Regexp
	config          *sarama.Config

	parser parsers.Parser
	mutex  sync.Mutex
	wg     sync.WaitGroup
	cancel context.CancelFunc
}

// client consumer
type StreamClient interface {
	Topics() ([]string, error)
	Close() error
}

type StreamClientCreator interface {
	Create(brokers []string, cfg *sarama.Config) (StreamClient, error)
}

type SaramaClientCreator struct {
}

func (*SaramaClientCreator) Create(brokers []string, cfg *sarama.Config) (StreamClient, error) {
	return sarama.NewClient(brokers, cfg)
}

// Consumer creator
type ConsumerGroup interface {
	Consume(ctx context.Context, topics []string, handler sarama.ConsumerGroupHandler) error
	Errors() <-chan error
	Close() error
}

type ConsumerGroupCreator interface {
	Create(brokers []string, group string, cfg *sarama.Config) (ConsumerGroup, error)
}

type SaramaConsumerCreator struct{}

func (*SaramaConsumerCreator) Create(brokers []string, group string, cfg *sarama.Config) (ConsumerGroup, error) {
	return sarama.NewConsumerGroup(brokers, group, cfg)
}

func (*KafkaConsumerDojot) SampleConfig() string {
	return sampleConfig
}

func (k *KafkaConsumerDojot) SetParser(parser parsers.Parser) {
	k.parser = parser
}

func (k *KafkaConsumerDojot) Init() error {
	if k.MaxUndeliveredMessages == 0 {
		k.MaxUndeliveredMessages = defaultMaxUndeliveredMessages
	}
	if time.Duration(k.MaxProcessingTime) == 0 {
		k.MaxProcessingTime = defaultMaxProcessingTime
	}
	if k.ConsumerGroup == "" {
		k.ConsumerGroup = defaultConsumerGroup
	}

	cfg := sarama.NewConfig()

	// Kafka version 0.10.2.0 is required for consumer groups.
	cfg.Version = sarama.V0_10_2_0

	if err := k.SetConfig(cfg); err != nil {
		return err
	}

	switch strings.ToLower(k.Offset) {
	case "oldest", "":
		cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	case "newest":
		cfg.Consumer.Offsets.Initial = sarama.OffsetNewest
	default:
		return fmt.Errorf("invalid offset %q", k.Offset)
	}

	switch strings.ToLower(k.BalanceStrategy) {
	case "range", "":
		cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRange
	case "roundrobin":
		cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategyRoundRobin
	case "sticky":
		cfg.Consumer.Group.Rebalance.Strategy = sarama.BalanceStrategySticky
	default:
		return fmt.Errorf("invalid balance strategy %q", k.BalanceStrategy)
	}

	if k.ConsumerCreator == nil {
		k.ConsumerCreator = &SaramaConsumerCreator{}
	}

	if k.ClientCreator == nil {
		k.ClientCreator = &SaramaClientCreator{}
	}

	cfg.Consumer.MaxProcessingTime = time.Duration(k.MaxProcessingTime)

	if k.ConsumerFetchDefault != 0 {
		cfg.Consumer.Fetch.Default = int32(k.ConsumerFetchDefault)
	}

	k.config = cfg
	return nil
}

func (k *KafkaConsumerDojot) Start(acc telegraf.Accumulator) error {
	var err error

	k.topicRegex, err = regexp.Compile(k.Topics[0])
	if err != nil {
		return nil
	}

	k.selectedTopics, err = k.selectTopics()
	if err != nil {
		return err
	}

	k.consumer, err = k.ConsumerCreator.Create(
		k.Brokers,
		k.ConsumerGroup,
		k.config,
	)
	if err != nil {
		return err
	}

	refleshInterval, err := time.ParseDuration(k.TopicRefreshInterval)
	if err != nil {
		return err
	}
	ticker := time.NewTicker(refleshInterval)
	go func() {
		for range ticker.C {
			k.Log.Debug("refreshing topics")
			selectedTopics, err := k.selectTopics()
			if err != nil {
				k.Log.Error("topic refresh failed: %v", err.Error())
			}

			k.mutex.Lock()
			k.selectedTopics = selectedTopics
			k.consumer.Close()
			k.consumer, err = k.ConsumerCreator.Create(
				k.Brokers,
				k.ConsumerGroup,
				k.config,
			)
			if err != nil {
				k.Log.Error(err.Error())
			}
			k.mutex.Unlock()
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	k.cancel = cancel

	// Start consumer goroutine
	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		for ctx.Err() == nil {
			k.Log.Debug("Initialize Consumer handler")
			handler := NewConsumerGroupHandler(acc, k.MaxUndeliveredMessages, k.parser, k.Log)
			handler.MaxMessageLen = k.MaxMessageLen
			handler.TopicTag = k.TopicTag
			err := k.consumer.Consume(ctx, k.selectedTopics, handler)
			if err != nil {
				acc.AddError(err)
				// Ignore returned error as we cannot do anything about it anyway
				//nolint:errcheck,revive
				internal.SleepContext(ctx, reconnectDelay)
			}
		}
		err = k.consumer.Close()
		if err != nil {
			acc.AddError(err)
		}
	}()

	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		for err := range k.consumer.Errors() {
			acc.AddError(err)
		}
	}()

	return nil
}

func (k *KafkaConsumerDojot) selectTopics() ([]string, error) {
	var err error

	k.client, err = k.ClientCreator.Create(
		k.Brokers,
		k.config,
	)
	defer k.client.Close()
	if err != nil {
		return nil, err
	}

	availableTopics, err := k.client.Topics()
	if err != nil {
		return nil, err
	}

	// Filter topics we care about
	var selectedTopics []string
	for _, t := range availableTopics {
		if k.topicRegex.MatchString(t) {
			k.Log.Debug("selected: " + t)
			selectedTopics = append(selectedTopics, t)
		}
	}

	return selectedTopics, nil
}

func (k *KafkaConsumerDojot) Gather(_ telegraf.Accumulator) error {
	return nil
}

func (k *KafkaConsumerDojot) Stop() {
	k.cancel()
	k.wg.Wait()
}

// Message is an aggregate type binding the Kafka message and the session so
// that offsets can be updated.
type Message struct {
	message *sarama.ConsumerMessage
	session sarama.ConsumerGroupSession
}

func NewConsumerGroupHandler(acc telegraf.Accumulator, maxUndelivered int, parser parsers.Parser, log telegraf.Logger) *ConsumerGroupHandler {
	handler := &ConsumerGroupHandler{
		acc:         acc.WithTracking(maxUndelivered),
		sem:         make(chan empty, maxUndelivered),
		undelivered: make(map[telegraf.TrackingID]Message, maxUndelivered),
		parser:      parser,
		log:         log,
	}
	return handler
}

// ConsumerGroupHandler is a sarama.ConsumerGroupHandler implementation.
type ConsumerGroupHandler struct {
	MaxMessageLen int
	TopicTag      string

	acc    telegraf.TrackingAccumulator
	sem    semaphore
	parser parsers.Parser
	wg     sync.WaitGroup
	cancel context.CancelFunc

	mu          sync.Mutex
	undelivered map[telegraf.TrackingID]Message

	log telegraf.Logger
}

// Setup is called once when a new session is opened.  It setups up the handler
// and begins processing delivered messages.
func (h *ConsumerGroupHandler) Setup(sarama.ConsumerGroupSession) error {
	h.undelivered = make(map[telegraf.TrackingID]Message)

	ctx, cancel := context.WithCancel(context.Background())
	h.cancel = cancel

	h.wg.Add(1)
	go func() {
		defer h.wg.Done()
		h.run(ctx)
	}()
	return nil
}

// Run processes any delivered metrics during the lifetime of the session.
func (h *ConsumerGroupHandler) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case track := <-h.acc.Delivered():
			h.onDelivery(track)
		}
	}
}

func (h *ConsumerGroupHandler) onDelivery(track telegraf.DeliveryInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()

	msg, ok := h.undelivered[track.ID()]
	if !ok {
		h.log.Errorf("Could not mark message delivered: %d", track.ID())
		return
	}

	if track.Delivered() {
		msg.session.MarkMessage(msg.message, "")
	}

	delete(h.undelivered, track.ID())
	<-h.sem
}

// Reserve blocks until there is an available slot for a new message.
func (h *ConsumerGroupHandler) Reserve(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case h.sem <- empty{}:
		return nil
	}
}

func (h *ConsumerGroupHandler) release() {
	<-h.sem
}

// Handle processes a message and if successful saves it to be acknowledged
// after delivery.
func (h *ConsumerGroupHandler) Handle(session sarama.ConsumerGroupSession, msg *sarama.ConsumerMessage) error {
	if h.MaxMessageLen != 0 && len(msg.Value) > h.MaxMessageLen {
		session.MarkMessage(msg, "")
		h.release()
		return fmt.Errorf("message exceeds max_message_len (actual %d, max %d)",
			len(msg.Value), h.MaxMessageLen)
	}

	metrics, err := h.parser.Parse(msg.Value)
	if err != nil {
		h.release()
		return err
	}

	if len(h.TopicTag) > 0 {
		for _, metric := range metrics {
			metric.AddTag(h.TopicTag, msg.Topic)
		}
	}

	h.mu.Lock()
	id := h.acc.AddTrackingMetricGroup(metrics)
	h.undelivered[id] = Message{session: session, message: msg}
	h.mu.Unlock()
	return nil
}

// ConsumeClaim is called once each claim in a goroutine and must be
// thread-safe.  Should run until the claim is closed.
func (h *ConsumerGroupHandler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	ctx := session.Context()

	for {
		err := h.Reserve(ctx)
		if err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			err := h.Handle(session, msg)
			if err != nil {
				h.acc.AddError(err)
			}
		}
	}
}

// Cleanup stops the internal goroutine and is called after all ConsumeClaim
// functions have completed.
func (h *ConsumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error {
	h.cancel()
	h.wg.Wait()
	return nil
}

func init() {
	inputs.Add("kafka_consumer_vdojot", func() telegraf.Input {
		return &KafkaConsumerDojot{}
	})
}
