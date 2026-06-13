package calls

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

func TestWriteAMIMessageSendUsesBase64Body(t *testing.T) {
	var out bytes.Buffer
	err := writeAMIMessageSend(&out, "abc123", Message{
		Destination: "pjsip:1001",
		To:          "sip:1001",
		From:        "Operator <sip:operator@example.test>",
		Body:        "hello\nworld",
	})
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Action: MessageSend\r\n",
		"ActionID: abc123\r\n",
		"Destination: pjsip:1001\r\n",
		"To: sip:1001\r\n",
		"From: Operator <sip:operator@example.test>\r\n",
		"Base64Body: aGVsbG8Kd29ybGQ=\r\n",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in %q", want, got)
		}
	}
}

func TestWaitAMIActionResponse(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("Event: FullyBooted\r\n\r\nResponse: Success\r\nActionID: target\r\n\r\n"))
	if err := waitAMIActionResponse(reader, "target"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitAMIActionResponseFailure(t *testing.T) {
	reader := bufio.NewReader(strings.NewReader("Response: Error\r\nActionID: target\r\nMessage: permission denied\r\n\r\n"))
	if err := waitAMIActionResponse(reader, "target"); err == nil || !strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("expected permission denied error, got %v", err)
	}
}
