package production

import (
	"context"
	"stationos.local/stationos/internal/announcements"
	"stationos.local/stationos/internal/djs"
)

// AnnouncementProducer prepares standalone speech, never claims that it aired.
type AnnouncementProducer interface {
	Announcement(context.Context, announcements.Message, djs.Profile) ([]byte, error)
}
