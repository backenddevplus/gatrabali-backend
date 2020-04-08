package push

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"

	"cloud.google.com/go/firestore"
	"firebase.google.com/go/messaging"
	"github.com/fiberweb/pubsub"
	"github.com/gofiber/fiber"

	"server/common/service"
	"server/common/types"
)

// Handler represents the handler for Push notification
type Handler struct {
	google *service.Google
}

// New returns an instance of Handler
func New(google *service.Google) *Handler {
	return &Handler{google}
}

// Handle handles the request
func (h *Handler) Handle() func(*fiber.Ctx) {
	return func(c *fiber.Ctx) {
		msg, ok := c.Locals(pubsub.LocalsKey).(*pubsub.Message)
		if !ok {
			c.Next(errors.New("unable to retrieve PubSub message from c.Locals"))
			return
		}

		// validate payload
		var payload types.PushNotificationPayload
		if err := json.Unmarshal(msg.Message.Data, &payload); err != nil {
			c.Next(err)
			return
		}
		if payload.Title == "" || payload.Body == "" || payload.UserID == "" {
			c.Next(errors.New("Invalid message payload: missing user_id, title or body"))
			return
		}

		ctx := context.Background()

		// init clients
		if err := h.google.InitFirestore(ctx); err != nil {
			c.Next(err)
			return
		}
		if err := h.google.InitMessaging(ctx); err != nil {
			c.Next(err)
			return
		}

		// preparing to push
		doc, err := h.google.Firestore.Collection("users").Doc(payload.UserID).Get(ctx)
		if err != nil {
			c.Next(err)
			return
		}
		user := doc.Data()
		tokens, ok := user["fcm_tokens"]
		if !ok {
			c.Next(fmt.Errorf("User %v doesn't have FCM tokens", payload.UserID))
			return
		}
		tokensMap := tokens.(map[string]interface{}) // convert to map
		if len(tokensMap) == 0 {
			c.Next(fmt.Errorf("User %v doesn't have FCM tokens", payload.UserID))
			return
		}

		// Android & iOS
		notification := &messaging.Notification{
			Title: payload.Title,
			Body:  payload.Body,
		}

		// -- Android sepcific config
		androidNotification := &messaging.AndroidNotification{
			Icon:  "https://firebasestorage.googleapis.com/v0/b/gatrabali.appspot.com/o/app%2Fnotification.png?alt=media&token=b76afe54-fc9c-4a05-addb-3f9eaaee7d2f",
			Color: "#4CB050",
		}
		androidConfig := messaging.AndroidConfig{
			Notification: androidNotification,
		}
		if payload.CollapseKey != "" {
			androidConfig.CollapseKey = payload.CollapseKey
		}
		// -- End Android sepcific config

		// loop through tokens and send the notification
		for token := range tokensMap {
			message := &messaging.Message{
				Data:         payload.Data,
				Notification: notification,
				Token:        token,
				Android:      &androidConfig,
			}

			resp, err := h.google.Messaging.Send(ctx, message)
			if err != nil {
				// if error, delete token
				log.Println("Notification not sent:", err)
				delete(tokensMap, token)
			} else {
				log.Println("Notification sent:", resp)
			}
		}

		// store back the remaining tokens to user document
		if _, err = h.google.Firestore.
			Collection("users").
			Doc(payload.UserID).
			Update(ctx, []firestore.Update{{Path: "fcm_tokens", Value: tokensMap}}); err != nil {

			c.Next(fmt.Errorf("Error saving fcm_tokens back to user doc: %v", err))
			return
		}
		c.SendStatus(http.StatusOK)
	}
}
