package groupassistant

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/chenhg5/cc-connect/core"
)

func TestMediaAdmissionRejectsBeforeReservationAndWhoamiSkipsAttachments(t *testing.T) {
	svc, _ := testService(t, nilRunner())
	for _, tc := range []struct {
		user, text string
		count      int
		want       error
	}{
		{"stranger", "work", 1, ErrDenied}, {"alice", "work", 21, ErrInvalid},
		{"alice", "/new", 1, ErrInvalid}, {"alice", "/compact", 1, ErrInvalid},
	} {
		if a, err := svc.AdmitMedia(context.Background(), memberMessage(tc.user, "id", tc.text), tc.count); !errors.Is(err, tc.want) || a != nil {
			t.Fatal(tc, a, err)
		}
	}
	msg := memberMessage("stranger", "who", "/whoami")
	if a, err := svc.AdmitMedia(context.Background(), msg, 1); a != nil || err != nil {
		t.Fatal(a, err)
	}
	if got := svc.Handle(context.Background(), msg); got.Text == svc.T(core.MsgGroupDenied) {
		t.Fatal(got)
	}
	if len(svc.slots) != 0 || len(svc.mediaDownloads) != 0 || len(svc.tasks) != 0 {
		t.Fatal("rejection retained resources")
	}
	svc.cfg.OwnerOnly = true
	if _, err := svc.AdmitMedia(context.Background(), memberMessage("alice", "trial", "work"), 1); !errors.Is(err, ErrDenied) {
		t.Fatal(err)
	}
}

func TestMediaReservationCoversDownloadAndReusesTaskDuringDelivery(t *testing.T) {
	calls := 0
	svc, _ := testService(t, func(context.Context, *Snapshot, string) (string, error) { calls++; return "finished", nil })
	svc.cfg.Groups = append(svc.cfg.Groups, "other")
	svc.cfg.Members["other"] = []string{"alice"}
	msg := memberMessage("alice", "media", "work")
	a, err := svc.AdmitMedia(context.Background(), msg, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(svc.slots) != 1 || len(svc.mediaDownloads) != 1 {
		t.Fatal("download not reserved")
	}
	other := memberMessage("alice", "other", "work")
	other.ChannelID = "other"
	if _, err := svc.AdmitMedia(context.Background(), other, 1); !errors.Is(err, errMemberBusy) {
		t.Fatal("cross-group bypass", err)
	}
	if _, err := svc.AdmitMedia(context.Background(), memberMessage("bob", "busy", "work"), 1); !errors.Is(err, errMediaBusy) {
		t.Fatal(err)
	}
	if len(svc.slots) != 1 {
		t.Fatal("busy rejection leaked a slot")
	}
	if got := svc.Handle(context.Background(), memberMessage("alice", "text", "work")); got.Text != svc.T(core.MsgGroupMemberBusy) {
		t.Fatal(got)
	}
	a.Downloaded()
	a.Downloaded()
	if len(svc.mediaDownloads) != 0 || len(svc.slots) != 1 {
		t.Fatal("handoff released task or retained downloader")
	}
	if got := svc.Handle(a.Context, msg); got.Text != "finished" || calls != 1 {
		t.Fatal("reserved delivery failed", got, calls)
	}
	if len(svc.slots) != 1 || len(svc.tasks) != 1 {
		t.Fatal("reservation ended before delivery completed")
	}
	a.Close()
	a.Close()
	if len(svc.slots) != 0 || len(svc.tasks) != 0 {
		t.Fatal("completed media retained resources")
	}
}

func TestMediaReservationsShareGlobalFiveTaskLimit(t *testing.T) {
	svc, _ := testService(t, nilRunner())
	var leases []*core.InboundMediaAdmission
	defer func() {
		for _, a := range leases {
			a.Close()
		}
	}()
	for i := 0; i < 5; i++ {
		a, err := svc.AdmitMedia(context.Background(), memberMessage("owner", "media", "work"), 1)
		if err != nil {
			t.Fatal(err)
		}
		leases = append(leases, a)
		a.Downloaded()
	}
	if _, err := svc.AdmitMedia(context.Background(), memberMessage("owner", "sixth", "work"), 1); !errors.Is(err, ErrBusy) {
		t.Fatal("sixth download admitted", err)
	}
	leases[0].Close()
	a, err := svc.AdmitMedia(context.Background(), memberMessage("alice", "next", "work"), 1)
	if err != nil {
		t.Fatal("completed slot not reusable", err)
	}
	a.Close()
}

func TestDisableOrStopCancelsDownloadingTask(t *testing.T) {
	for _, action := range []string{"disable", "stop", "shutdown"} {
		t.Run(action, func(t *testing.T) {
			svc, _ := testService(t, nilRunner())
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			a, err := svc.AdmitMedia(ctx, memberMessage("alice", "cancel", "work"), 1)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			stopped := make(chan struct{})
			go func() { <-a.Context.Done(); a.Close(); close(stopped) }()
			if action == "disable" {
				if err := svc.ApplyMember(ctx, "group", "alice", false, func() error { return nil }); err != nil {
					t.Fatal(err)
				}
			} else if action == "stop" {
				var code string
				svc.tasksMu.Lock()
				for key := range svc.tasks {
					code = key
				}
				svc.tasksMu.Unlock()
				if got := svc.LocalTaskCommand(ctx, Identity{"group", "wecom:owner"}, "stop", code); !got.ControlOK {
					t.Fatal(got)
				}
			} else {
				cancel()
			}
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				t.Fatal("download not canceled")
			}
			if len(svc.slots) != 0 || len(svc.mediaDownloads) != 0 {
				t.Fatal("canceled download retained resources")
			}
			if !svc.MemberCleanupReady("group", "alice") {
				t.Fatal("canceled task cleanup unconfirmed")
			}
		})
	}
}
