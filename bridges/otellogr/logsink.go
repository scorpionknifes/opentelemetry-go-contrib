// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package otellogr // import "go.opentelemetry.io/contrib/bridges/otellogr"

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"go.opentelemetry.io/otel/log"
)

const (
	bridgeName = "go.opentelemetry.io/contrib/bridges/otellogr"

	// nameKey is used to log the `WithName` values as an additional attribute.
	nameKey = "logger"
	// errKey is used to log the error parameter of Error as an additional attribute.
	errKey = "err"
)

// NewLogSink returns a new [LogSink] to be used as a [logr.LogSink].
//
// If [WithLoggerProvider] is not provided, the returned LogSink will use the
// global LoggerProvider.
func NewLogSink(options ...Option) *LogSink {
	c := newConfig(options)
	return &LogSink{
		logger: c.logger(),
	}
}

// LogSink is a [logr.LogSink] that sends all logging records it receives to
// OpenTelemetry. See package documentation for how conversions are made.
type LogSink struct {
	name   string
	logger log.Logger
	values []log.KeyValue
}

// Compile-time check *Handler implements logr.LogSink.
var _ logr.LogSink = (*LogSink)(nil)

// log sends a log record to the OpenTelemetry logger.
func (l *LogSink) log(err error, msg string, serverity log.Severity, kvList ...any) {
	var record log.Record
	record.SetTimestamp(time.Now())
	record.SetBody(log.StringValue(msg))
	record.SetSeverity(serverity)

	if l.name != "" {
		record.AddAttributes(log.String(nameKey, l.name))
	}

	if err != nil {
		record.AddAttributes(log.KeyValue{
			Key:   errKey,
			Value: convertValue(err),
		})
	}

	if len(l.values) > 0 {
		record.AddAttributes(l.values...)
	}

	kv := convertKVs(kvList)
	if len(kv) > 0 {
		record.AddAttributes(kv...)
	}

	ctx := context.Background()
	l.logger.Emit(ctx, record)
}

// Enabled tests whether this LogSink is enabled at the specified V-level.
// For example, commandline flags might be used to set the logging
// verbosity and disable some info logs.
func (l *LogSink) Enabled(level int) bool {
	var record log.Record
	const sevOffset = int(log.SeverityDebug)
	record.SetSeverity(log.Severity(level + sevOffset))
	ctx := context.Background()
	return l.logger.Enabled(ctx, record)
}

// Error logs an error, with the given message and key/value pairs.
func (l *LogSink) Error(err error, msg string, keysAndValues ...any) {
	const severity = log.SeverityError

	l.log(err, msg, severity, keysAndValues...)
}

// Info logs a non-error message with the given key/value pairs.
func (l *LogSink) Info(level int, msg string, keysAndValues ...any) {
	const sevOffset = int(log.SeverityInfo)
	severity := log.Severity(sevOffset + level)

	l.log(nil, msg, severity, keysAndValues...)
}

// Init receives optional information about the logr library this
// implementation does not use it.
func (l *LogSink) Init(info logr.RuntimeInfo) {
	// We don't need to do anything here.
	// CallDepth is used to calculate the caller's PC.
	// PC is dropped as part of the conversion to the OpenTelemetry log.Record.
}

// WithName returns a new LogSink with the specified name appended.
func (l LogSink) WithName(name string) logr.LogSink {
	if len(l.name) > 0 {
		l.name += "/"
	}
	l.name += name
	return &l
}

// WithValues returns a new LogSink with additional key/value pairs.
func (l LogSink) WithValues(keysAndValues ...any) logr.LogSink {
	attrs := convertKVs(keysAndValues)
	l.values = append(l.values, attrs...)
	return &l
}

func convertKVs(keysAndValues []any) []log.KeyValue {
	if len(keysAndValues) == 0 {
		return nil
	}
	if len(keysAndValues)%2 != 0 {
		// Ensure an odd number of items here does not corrupt the list
		keysAndValues = append(keysAndValues, nil)
	}

	kv := make([]log.KeyValue, 0, len(keysAndValues)/2)
	for i := 0; i < len(keysAndValues); i += 2 {
		k, ok := keysAndValues[i].(string)
		if !ok {
			// Ensure that the key is a string
			k = fmt.Sprintf("%v", keysAndValues[i])
		}
		kv = append(kv, log.KeyValue{
			Key:   k,
			Value: convertValue(keysAndValues[i+1]),
		})
	}
	return kv
}

func convertValue(v any) log.Value {
	// Handling the most common types without reflect is a small perf win.
	switch val := v.(type) {
	case bool:
		return log.BoolValue(val)
	case string:
		return log.StringValue(val)
	case int:
		return log.Int64Value(int64(val))
	case int8:
		return log.Int64Value(int64(val))
	case int16:
		return log.Int64Value(int64(val))
	case int32:
		return log.Int64Value(int64(val))
	case int64:
		return log.Int64Value(val)
	case uint:
		return convertUintValue(uint64(val))
	case uint8:
		return log.Int64Value(int64(val))
	case uint16:
		return log.Int64Value(int64(val))
	case uint32:
		return log.Int64Value(int64(val))
	case uint64:
		return convertUintValue(val)
	case uintptr:
		return convertUintValue(uint64(val))
	case float32:
		return log.Float64Value(float64(val))
	case float64:
		return log.Float64Value(val)
	case time.Duration:
		return log.Int64Value(val.Nanoseconds())
	case complex64:
		return log.StringValue(strconv.FormatComplex(complex128(val), 'f', -1, 64))
	case complex128:
		return log.StringValue(strconv.FormatComplex(val, 'f', -1, 128))
	case time.Time:
		return log.Int64Value(val.UnixNano())
	case []byte:
		return log.BytesValue(val)
	case error:
		return log.StringValue(fmt.Sprintf("%+v", val))
	}

	t := reflect.TypeOf(v)
	if t == nil {
		return log.Value{}
	}
	val := reflect.ValueOf(v)
	switch t.Kind() {
	case reflect.Struct:
		return log.StringValue(fmt.Sprintf("%+v", v))
	case reflect.Slice, reflect.Array:
		items := make([]log.Value, 0, val.Len())
		for i := 0; i < val.Len(); i++ {
			items = append(items, convertValue(val.Index(i).Interface()))
		}
		return log.SliceValue(items...)
	case reflect.Map:
		kvs := make([]log.KeyValue, 0, val.Len())
		for _, k := range val.MapKeys() {
			kvs = append(kvs, log.KeyValue{
				Key:   fmt.Sprintf("%v", k.Interface()),
				Value: convertValue(val.MapIndex(k).Interface()),
			})
		}
		return log.MapValue(kvs...)
	case reflect.Ptr, reflect.Interface:
		if val.IsNil() {
			return log.Value{}
		}
		return convertValue(val.Elem().Interface())
	}

	// Try to handle this as gracefully as possible.
	//
	// Don't panic here. it is preferable to have user's open issue
	// asking why their attributes have a "unhandled: " prefix than
	// say that their code is panicking.
	return log.StringValue(fmt.Sprintf("unhandled: (%s) %+v", t, v))
}

func convertUintValue(v uint64) log.Value {
	if v > math.MaxInt64 {
		return log.StringValue(strconv.FormatUint(v, 10))
	}
	return log.Int64Value(int64(v))
}
