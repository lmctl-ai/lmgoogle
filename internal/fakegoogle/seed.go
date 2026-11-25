// SPDX-License-Identifier: MIT

package fakegoogle

import (
	"encoding/base64"

	"google.golang.org/api/analyticsadmin/v1beta"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/youtube/v3"
)

func (s *Server) seed() {
	// --- Google Analytics 4 Accounts, Properties, DataStreams, CustomDimensions, KeyEvents ---
	s.accounts["accounts/1001"] = &analyticsadmin.GoogleAnalyticsAdminV1betaAccount{
		Name:        "accounts/1001",
		DisplayName: "Acme Digital Media",
		RegionCode:  "US",
		CreateTime:  "2024-01-15T10:00:00Z",
		UpdateTime:  "2026-09-01T12:00:00Z",
	}
	s.accounts["accounts/1002"] = &analyticsadmin.GoogleAnalyticsAdminV1betaAccount{
		Name:        "accounts/1002",
		DisplayName: "Sandbox Labs",
		RegionCode:  "US",
		CreateTime:  "2025-03-10T08:30:00Z",
		UpdateTime:  "2026-10-01T09:00:00Z",
	}

	s.properties["properties/3001001"] = &analyticsadmin.GoogleAnalyticsAdminV1betaProperty{
		Name:             "properties/3001001",
		Parent:           "accounts/1001",
		Account:          "accounts/1001",
		DisplayName:      "Acme Web Storefront",
		PropertyType:     "PROPERTY_TYPE_ORDINARY",
		TimeZone:         "America/Chicago",
		CurrencyCode:     "USD",
		IndustryCategory: "SHOPPING",
		ServiceLevel:     "GOOGLE_ANALYTICS_STANDARD",
		CreateTime:       "2024-01-15T10:05:00Z",
		UpdateTime:       "2026-10-12T15:00:00Z",
	}
	s.properties["properties/3001002"] = &analyticsadmin.GoogleAnalyticsAdminV1betaProperty{
		Name:             "properties/3001002",
		Parent:           "accounts/1001",
		Account:          "accounts/1001",
		DisplayName:      "Acme Docs & Developer Portal",
		PropertyType:     "PROPERTY_TYPE_ORDINARY",
		TimeZone:         "America/New_York",
		CurrencyCode:     "USD",
		IndustryCategory: "TECHNOLOGY",
		ServiceLevel:     "GOOGLE_ANALYTICS_STANDARD",
		CreateTime:       "2024-06-20T14:00:00Z",
		UpdateTime:       "2026-10-20T11:30:00Z",
	}
	s.properties["properties/3002001"] = &analyticsadmin.GoogleAnalyticsAdminV1betaProperty{
		Name:             "properties/3002001",
		Parent:           "accounts/1002",
		Account:          "accounts/1002",
		DisplayName:      "Sandbox Mobile Telemetry",
		PropertyType:     "PROPERTY_TYPE_ORDINARY",
		TimeZone:         "UTC",
		CurrencyCode:     "USD",
		IndustryCategory: "INTERNET_AND_TELECOM",
		ServiceLevel:     "GOOGLE_ANALYTICS_STANDARD",
		CreateTime:       "2025-03-10T09:00:00Z",
		UpdateTime:       "2026-10-05T16:45:00Z",
	}

	s.dataStreams["properties/3001001/dataStreams/5001"] = &analyticsadmin.GoogleAnalyticsAdminV1betaDataStream{
		Name:        "properties/3001001/dataStreams/5001",
		Type:        "WEB_DATA_STREAM",
		DisplayName: "Acme Main Web",
		WebStreamData: &analyticsadmin.GoogleAnalyticsAdminV1betaDataStreamWebStreamData{
			MeasurementId: "G-ACME1001WEB",
			DefaultUri:    "https://shop.acme.example",
		},
	}
	s.dataStreams["properties/3001002/dataStreams/5002"] = &analyticsadmin.GoogleAnalyticsAdminV1betaDataStream{
		Name:        "properties/3001002/dataStreams/5002",
		Type:        "WEB_DATA_STREAM",
		DisplayName: "Acme Docs Web",
		WebStreamData: &analyticsadmin.GoogleAnalyticsAdminV1betaDataStreamWebStreamData{
			MeasurementId: "G-ACME1002DOC",
			DefaultUri:    "https://docs.acme.example",
		},
	}
	s.dataStreams["properties/3002001/dataStreams/5003"] = &analyticsadmin.GoogleAnalyticsAdminV1betaDataStream{
		Name:        "properties/3002001/dataStreams/5003",
		Type:        "IOS_APP_DATA_STREAM",
		DisplayName: "Sandbox iOS App",
		IosAppStreamData: &analyticsadmin.GoogleAnalyticsAdminV1betaDataStreamIosAppStreamData{
			FirebaseAppId: "1:3002001:ios:abc123",
			BundleId:      "example.sandbox.app",
		},
	}

	s.customDims["properties/3001001/customDimensions/cd1"] = &analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension{
		Name:          "properties/3001001/customDimensions/cd1",
		ParameterName: "plan_tier",
		DisplayName:   "Plan Tier",
		Scope:         "USER",
		Description:   "Customer subscription tier",
	}
	s.customDims["properties/3001001/customDimensions/cd2"] = &analyticsadmin.GoogleAnalyticsAdminV1betaCustomDimension{
		Name:          "properties/3001001/customDimensions/cd2",
		ParameterName: "campaign_source",
		DisplayName:   "Campaign Source",
		Scope:         "EVENT",
		Description:   "Attribution source parameter",
	}

	s.keyEvents["properties/3001001/keyEvents/purchase"] = &analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent{
		Name:           "properties/3001001/keyEvents/purchase",
		EventName:      "purchase",
		CountingMethod: "ONCE_PER_EVENT",
	}
	s.keyEvents["properties/3001001/keyEvents/sign_up"] = &analyticsadmin.GoogleAnalyticsAdminV1betaKeyEvent{
		Name:           "properties/3001001/keyEvents/sign_up",
		EventName:      "sign_up",
		CountingMethod: "ONCE_PER_SESSION",
	}

	// --- GA4 Reporting rows (deterministic traffic dataset per property) ---
	s.gaRows["properties/3001001"] = []gaSampleRow{
		{Country: "United States", Device: "desktop", Channel: "Organic Search", Date: "20261108", ActiveUsers: 4210, Sessions: 5320, ScreenPageViews: 14820, Conversions: 184, Revenue: 12450.50},
		{Country: "United States", Device: "mobile", Channel: "Paid Search", Date: "20261108", ActiveUsers: 3180, Sessions: 3910, ScreenPageViews: 8640, Conversions: 112, Revenue: 7420.00},
		{Country: "Canada", Device: "desktop", Channel: "Organic Search", Date: "20261109", ActiveUsers: 1140, Sessions: 1420, ScreenPageViews: 4120, Conversions: 49, Revenue: 3290.25},
		{Country: "United Kingdom", Device: "mobile", Channel: "Organic Video", Date: "20261109", ActiveUsers: 980, Sessions: 1210, ScreenPageViews: 3110, Conversions: 38, Revenue: 2150.75},
		{Country: "Germany", Device: "desktop", Channel: "Direct", Date: "20261110", ActiveUsers: 860, Sessions: 1090, ScreenPageViews: 2950, Conversions: 31, Revenue: 1890.00},
		{Country: "Japan", Device: "mobile", Channel: "Referral", Date: "20261111", ActiveUsers: 640, Sessions: 790, ScreenPageViews: 1980, Conversions: 22, Revenue: 1340.00},
	}
	s.gaRows["properties/3001002"] = []gaSampleRow{
		{Country: "United States", Device: "desktop", Channel: "Organic Search", Date: "20261108", ActiveUsers: 2890, Sessions: 3540, ScreenPageViews: 11200, Conversions: 95, Revenue: 0},
		{Country: "India", Device: "desktop", Channel: "Organic Search", Date: "20261109", ActiveUsers: 1920, Sessions: 2310, ScreenPageViews: 7850, Conversions: 42, Revenue: 0},
		{Country: "Germany", Device: "desktop", Channel: "Direct", Date: "20261110", ActiveUsers: 740, Sessions: 910, ScreenPageViews: 3040, Conversions: 18, Revenue: 0},
	}
	s.gaRows["properties/3002001"] = []gaSampleRow{
		{Country: "United States", Device: "mobile", Channel: "Direct", Date: "20261110", ActiveUsers: 1520, Sessions: 2180, ScreenPageViews: 6400, Conversions: 64, Revenue: 980.00},
	}

	// --- YouTube Data API v3 Seed Data ---
	s.channels["UC_ACME_DEV"] = &youtube.Channel{
		Kind: "youtube#channel",
		Id:   "UC_ACME_DEV",
		Snippet: &youtube.ChannelSnippet{
			Title:       "Acme Engineering",
			Description: "Systems engineering, developer tooling, and product walkthroughs.",
			CustomUrl:   "@acmeengineering",
			Country:     "US",
			PublishedAt: "2022-05-12T16:00:00Z",
		},
		Statistics: &youtube.ChannelStatistics{
			SubscriberCount: 48200,
			ViewCount:       1945000,
			VideoCount:      4,
		},
		ContentDetails: &youtube.ChannelContentDetails{
			RelatedPlaylists: &youtube.ChannelContentDetailsRelatedPlaylists{
				Uploads: "UU_ACME_DEV",
			},
		},
	}
	s.myChannelID = "UC_ACME_DEV"

	s.videos["vid_go_cli_01"] = &youtube.Video{
		Kind: "youtube#video",
		Id:   "vid_go_cli_01",
		Snippet: &youtube.VideoSnippet{
			Title:        "Building Zero-Dependency Go CLIs",
			Description:  "How to compile static Go CLI hosts and bind typed scripting schemas.",
			ChannelId:    "UC_ACME_DEV",
			ChannelTitle: "Acme Engineering",
			PublishedAt:  "2026-10-15T14:00:00Z",
			CategoryId:   "28",
			Tags:         []string{"golang", "cli", "lmscript"},
		},
		ContentDetails: &youtube.VideoContentDetails{
			Duration: "PT14M32S",
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: "public",
			UploadStatus:  "processed",
		},
		Statistics: &youtube.VideoStatistics{
			ViewCount:    84500,
			LikeCount:    4120,
			CommentCount: 215,
		},
	}
	s.videos["vid_ga4_api_02"] = &youtube.Video{
		Kind: "youtube#video",
		Id:   "vid_ga4_api_02",
		Snippet: &youtube.VideoSnippet{
			Title:        "Automating GA4 Reports with Go & OAuth2",
			Description:  "End-to-end walkthrough of Google Analytics Admin and Data APIs.",
			ChannelId:    "UC_ACME_DEV",
			ChannelTitle: "Acme Engineering",
			PublishedAt:  "2026-10-28T17:30:00Z",
			CategoryId:   "28",
			Tags:         []string{"ga4", "analytics", "oauth2", "golang"},
		},
		ContentDetails: &youtube.VideoContentDetails{
			Duration: "PT18M05S",
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: "public",
			UploadStatus:  "processed",
		},
		Statistics: &youtube.VideoStatistics{
			ViewCount:    52300,
			LikeCount:    2890,
			CommentCount: 142,
		},
	}
	s.videos["vid_yt_upload_03"] = &youtube.Video{
		Kind: "youtube#video",
		Id:   "vid_yt_upload_03",
		Snippet: &youtube.VideoSnippet{
			Title:        "YouTube Data API v3 Upload Pipelines",
			Description:  "Resumable uploads, playlists, and analytics monitoring.",
			ChannelId:    "UC_ACME_DEV",
			ChannelTitle: "Acme Engineering",
			PublishedAt:  "2026-11-04T12:00:00Z",
			CategoryId:   "28",
			Tags:         []string{"youtube", "api", "automation"},
		},
		ContentDetails: &youtube.VideoContentDetails{
			Duration: "PT11M18S",
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: "unlisted",
			UploadStatus:  "processed",
		},
		Statistics: &youtube.VideoStatistics{
			ViewCount:    12900,
			LikeCount:    780,
			CommentCount: 38,
		},
	}
	s.videos["vid_launch_04"] = &youtube.Video{
		Kind: "youtube#video",
		Id:   "vid_launch_04",
		Snippet: &youtube.VideoSnippet{
			Title:        "Acme Q4 Release Keynote",
			Description:  "Live keynote announcing new developer workflows.",
			ChannelId:    "UC_ACME_DEV",
			ChannelTitle: "Acme Engineering",
			PublishedAt:  "2026-11-10T18:00:00Z",
			CategoryId:   "28",
			Tags:         []string{"keynote", "release"},
		},
		ContentDetails: &youtube.VideoContentDetails{
			Duration: "PT32M45S",
		},
		Status: &youtube.VideoStatus{
			PrivacyStatus: "public",
			UploadStatus:  "processed",
		},
		Statistics: &youtube.VideoStatistics{
			ViewCount:    124000,
			LikeCount:    6450,
			CommentCount: 410,
		},
	}

	s.playlists["PL_ACME_TUTORIALS"] = &youtube.Playlist{
		Kind: "youtube#playlist",
		Id:   "PL_ACME_TUTORIALS",
		Snippet: &youtube.PlaylistSnippet{
			Title:        "Developer Tutorials",
			Description:  "Go SDK and automation tutorials",
			ChannelId:    "UC_ACME_DEV",
			ChannelTitle: "Acme Engineering",
			PublishedAt:  "2026-09-01T10:00:00Z",
		},
		Status: &youtube.PlaylistStatus{
			PrivacyStatus: "public",
		},
		ContentDetails: &youtube.PlaylistContentDetails{
			ItemCount: 3,
		},
	}
	s.playlistItems["PL_ACME_TUTORIALS"] = []string{"vid_go_cli_01", "vid_ga4_api_02", "vid_yt_upload_03"}
	s.playlistItems["UU_ACME_DEV"] = []string{"vid_launch_04", "vid_yt_upload_03", "vid_ga4_api_02", "vid_go_cli_01"}

	s.comments["vid_go_cli_01"] = []*youtube.CommentThread{
		{
			Kind: "youtube#commentThread",
			Id:   "ct_001",
			Snippet: &youtube.CommentThreadSnippet{
				VideoId: "vid_go_cli_01",
				TopLevelComment: &youtube.Comment{
					Id: "c_001",
					Snippet: &youtube.CommentSnippet{
						AuthorDisplayName: "dev_alex",
						TextDisplay:       "Super clean schema design, compiles fast!",
						PublishedAt:       "2026-10-16T09:15:00Z",
					},
				},
			},
		},
		{
			Kind: "youtube#commentThread",
			Id:   "ct_002",
			Snippet: &youtube.CommentThreadSnippet{
				VideoId: "vid_go_cli_01",
				TopLevelComment: &youtube.Comment{
					Id: "c_002",
					Snippet: &youtube.CommentSnippet{
						AuthorDisplayName: "sre_priya",
						TextDisplay:       "Love that it runs offline against the in-process fake too.",
						PublishedAt:       "2026-10-17T14:22:00Z",
					},
				},
			},
		},
	}

	// --- YouTube Analytics v2 Seed Data ---
	s.ytAnalyticsRows = []ytAnalyticsRow{
		{Day: "2026-11-08", VideoID: "vid_go_cli_01", Country: "US", Views: 4120, EstimatedMinutesWatched: 31400, AverageViewDuration: 457, Likes: 210, Comments: 14, SubscribersGained: 86},
		{Day: "2026-11-09", VideoID: "vid_ga4_api_02", Country: "US", Views: 3680, EstimatedMinutesWatched: 28900, AverageViewDuration: 471, Likes: 195, Comments: 11, SubscribersGained: 74},
		{Day: "2026-11-10", VideoID: "vid_launch_04", Country: "US", Views: 9840, EstimatedMinutesWatched: 112500, AverageViewDuration: 686, Likes: 620, Comments: 48, SubscribersGained: 245},
		{Day: "2026-11-11", VideoID: "vid_launch_04", Country: "CA", Views: 2450, EstimatedMinutesWatched: 26400, AverageViewDuration: 646, Likes: 148, Comments: 9, SubscribersGained: 52},
		{Day: "2026-11-12", VideoID: "vid_yt_upload_03", Country: "GB", Views: 1190, EstimatedMinutesWatched: 7850, AverageViewDuration: 395, Likes: 64, Comments: 5, SubscribersGained: 21},
	}

	// --- Gmail v1 Seed Data ---
	s.gmailEmail = "engineering@acme.example"
	for _, lbl := range []*gmail.Label{
		{Id: "INBOX", Name: "INBOX", Type: "system", MessagesTotal: 3, MessagesUnread: 2},
		{Id: "SENT", Name: "SENT", Type: "system", MessagesTotal: 1, MessagesUnread: 0},
		{Id: "UNREAD", Name: "UNREAD", Type: "system", MessagesTotal: 2, MessagesUnread: 2},
		{Id: "IMPORTANT", Name: "IMPORTANT", Type: "system", MessagesTotal: 2, MessagesUnread: 1},
		{Id: "STARRED", Name: "STARRED", Type: "system", MessagesTotal: 1, MessagesUnread: 1},
		{Id: "TRASH", Name: "TRASH", Type: "system", MessagesTotal: 0, MessagesUnread: 0},
		{Id: "Label_alerts", Name: "alerts", Type: "user", MessagesTotal: 2, MessagesUnread: 1},
	} {
		s.gmailLabels[lbl.Id] = lbl
	}

	b64 := func(txt string) string {
		return base64.RawURLEncoding.EncodeToString([]byte(txt))
	}
	seedMsgs := []*gmail.Message{
		{
			Id:       "msg_1001",
			ThreadId: "thr_1001",
			LabelIds: []string{"INBOX", "UNREAD", "IMPORTANT", "Label_alerts"},
			Snippet:  "GA4 weekly revenue anomaly: Organic Video conversions rose 34% to $2,940.00.",
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "alerts@acme.example"},
					{Name: "To", Value: "engineering@acme.example"},
					{Name: "Subject", Value: "[GA4 Alert] Organic Video revenue spike on 3001001"},
					{Name: "Date", Value: "Mon, 23 Nov 2026 09:15:00 -0600"},
				},
				Body: &gmail.MessagePartBody{
					Data: b64("GA4 property 3001001 report:\nOrganic Video activeUsers=980, conversions=92, totalRevenue=$2940.00.\nTop video driving referral clicks: vid_launch_04."),
				},
			},
		},
		{
			Id:       "msg_1002",
			ThreadId: "thr_1002",
			LabelIds: []string{"INBOX", "UNREAD", "STARRED"},
			Snippet:  "Release checklist: lmgoogle 0.1.0 keynote crossed 214,800 views.",
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "alex@acme.example"},
					{Name: "To", Value: "engineering@acme.example"},
					{Name: "Subject", Value: "Re: lmgoogle 0.1.0 keynote metrics"},
					{Name: "Date", Value: "Sun, 22 Nov 2026 16:40:00 -0600"},
				},
				Body: &gmail.MessagePartBody{
					Data: b64("Hi team,\n\nvid_launch_04 now has 214,800 views and 11,800 likes on YouTube.\nCan someone export the weekly GA4 breakdown?"),
				},
			},
		},
		{
			Id:       "msg_1003",
			ThreadId: "thr_1001",
			LabelIds: []string{"INBOX", "IMPORTANT", "Label_alerts"},
			Snippet:  "Automated quota check: Analytics & YouTube usage well within daily limits.",
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "ops-bot@acme.example"},
					{Name: "To", Value: "engineering@acme.example"},
					{Name: "Subject", Value: "Re: [GA4 Alert] Daily quota health check OK"},
					{Name: "Date", Value: "Mon, 23 Nov 2026 10:02:00 -0600"},
				},
				Body: &gmail.MessagePartBody{
					Data: b64("Daily API quota usage is below 5% across GA4 and YouTube Data v3."),
				},
			},
		},
		{
			Id:       "msg_1004",
			ThreadId: "thr_1004",
			LabelIds: []string{"SENT"},
			Snippet:  "Uploaded demo keynote draft vid_yt_upload_03 for review.",
			Payload: &gmail.MessagePart{
				MimeType: "text/plain",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "engineering@acme.example"},
					{Name: "To", Value: "alex@acme.example"},
					{Name: "Subject", Value: "Upload draft ready for review"},
					{Name: "Date", Value: "Sat, 21 Nov 2026 14:20:00 -0600"},
				},
				Body: &gmail.MessagePartBody{
					Data: b64("Draft video vid_yt_upload_03 is unlisted and ready for review."),
				},
			},
		},
		{
			Id:       "msg_1005",
			ThreadId: "thr_1005",
			LabelIds: []string{"INBOX", "IMPORTANT"},
			Snippet:  "Order confirmation #ORDER-1005 and attached receipt.",
			Payload: &gmail.MessagePart{
				MimeType: "multipart/mixed",
				Headers: []*gmail.MessagePartHeader{
					{Name: "From", Value: "billing@acme.example"},
					{Name: "To", Value: "engineering@acme.example"},
					{Name: "Subject", Value: "Receipt for order #ORDER-1005"},
					{Name: "Date", Value: "Mon, 23 Nov 2026 11:30:00 -0600"},
				},
				Parts: []*gmail.MessagePart{
					{
						MimeType: "text/plain",
						Body: &gmail.MessagePartBody{
							Data: b64("Hello,\n\nThank you for order #ORDER-1005.\nTotal: $149.00 USD.\nSee attached receipt_ORDER-1005.txt."),
						},
					},
					{
						Filename: "receipt_ORDER-1005.txt",
						MimeType: "text/plain",
						Body: &gmail.MessagePartBody{
							AttachmentId: "att_ORDER-1005",
							Size:         54,
						},
					},
				},
			},
		},
	}
	s.gmailAttachments["msg_1005/att_ORDER-1005"] = []byte("Order: ORDER-1005\nAmount: $149.00 USD\nStatus: PAID\n")
	for _, msg := range seedMsgs {
		s.gmailMessages[msg.Id] = msg
		s.gmailOrder = append(s.gmailOrder, msg.Id)
	}
}
