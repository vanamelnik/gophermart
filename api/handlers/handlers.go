package handlers

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/vanamelnik/gophermart/model"
	appContext "github.com/vanamelnik/gophermart/pkg/ctx"
	"github.com/vanamelnik/gophermart/service/gophermart"
	"github.com/vanamelnik/gophermart/storage"
)

const (
	rememberTokenSize = 32
	cookieName        = "gophermart_remember"
)

// Handlers represents API handlers.
type Handlers struct {
	svc gophermart.Service
	db  storage.Storage
}

func New(svc gophermart.Service, db storage.Storage) Handlers {
	return Handlers{
		svc: svc,
		db:  db,
	}
}

// User represents json request to the service with user's login and password.
type User struct {
	Login    string `json:"login"`
	Password string `json:"password"`
}

// Register — user registration.
//
// POST /api/user/register
func (h Handlers) Register(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "Register").Logger()
	if !checkContentType(r, "application/json") {
		log.Error().Msg("wrong Content-type")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}
	u := User{}
	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()
	if err := dec.Decode(&u); err != nil {
		log.Error().Err(err).Msg("unmarshalling request body")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}

	// Create a new user entry in DB.
	user, err := h.svc.Create(r.Context(), u.Login, u.Password)
	if err != nil {
		if errors.Is(err, storage.ErrLoginAlreadyExists) {
			log.Error().Err(err).Msg("could not create the user")
			http.Error(w, "Login already exists", http.StatusConflict)

			return
		}
		log.Error().Err(err).Msg("creating a new user")
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	if err := h.signIn(w, r, user); err != nil {
		log.Error().Err(err).Msg("could not authenticate the user")
		http.Error(w, "Internal server error - could not authenticate the user.", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// Login — user authentication.
//
// POST /api/user/login
func (h Handlers) Login(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "Login").Logger()
	if !checkContentType(r, "application/json") {
		log.Error().Msg("wrong Content-type")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}

	u := User{}
	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()
	if err := dec.Decode(&u); err != nil {
		log.Error().Err(err).Msg("unmarshalling request body")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}

	user, err := h.svc.Authenticate(r.Context(), u.Login, u.Password)
	if err != nil {
		if errors.Is(err, gophermart.ErrWrongPassword) {
			log.Error().Err(err).Msg("authenticate: ")
			http.Error(w, "Unauthorized", http.StatusUnauthorized)

			return
		}
		log.Error().Err(err).Msg("authenticate: ")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}

	if err := h.signIn(w, r, user); err != nil {
		log.Error().Err(err).Msg("authenticate: ")
		http.Error(w, "Internal server error - could not authenticate the user.", http.StatusInternalServerError)

		return
	}

	w.WriteHeader(http.StatusOK)
}

// PostOrder — load an order number to calculate.
//
// POST /api/user/orders
func (h Handlers) PostOrder(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "PostOrder").Logger()
	body, err := io.ReadAll(r.Body)
	if err != nil || r.ContentLength <= 0 || !checkContentType(r, "text/plain") {
		log.Error().Err(err).Msg("reading body")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}
	defer r.Body.Close()
	orderID := model.OrderID(body)
	if !orderID.Valid() {
		log.Error().Msgf("Invalid order number: %s", orderID.String())
		http.Error(w, "Incorrect order number format", http.StatusUnprocessableEntity)

		return
	}
	log = log.With().Str("orderID", orderID.String()).Logger()

	// Process the order.
	err = h.svc.ProcessOrder(r.Context(), orderID)
	switch {
	case err == nil:
		log.Info().Msg("order processed")
		w.WriteHeader(http.StatusAccepted)
	case errors.Is(err, gophermart.ErrOrderExecutedBySameUser):
		log.Warn().Err(err).Msg("process order")
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, gophermart.ErrOrderExecutedByAnotherUser):
		log.Error().Err(err).Msg("process order:")
		http.Error(w, "The order is already executed by another user", http.StatusConflict)
	default:
		log.Error().Err(err).Msg("process order:")
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// GetOrders — get list of uploaded order numbers, processing status and accrual information.
//
// GET /api/user/orders
func (h Handlers) GetOrders(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "GetOrders").Logger()

	orders, err := h.svc.GetOrders(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("get user orders")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}
	if len(orders) == 0 {
		log.Warn().Msg("no orders")
		http.Error(w, "no orders found", http.StatusNoContent)

		return
	}
	log.Trace().Msgf("orders: %v", orders)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(orders); err != nil {
		log.Error().Err(err).Msg("marshalling orders list")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

}

// GetBalance — get current user loyalty point balance.
//
// GET /api/user/balance
func (h Handlers) GetBalance(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "GetBalance").Logger()

	balance, err := h.svc.GetBalance(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("fetching balance info")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(balance); err != nil {
		log.Error().Err(err).Msg("marshalling balance")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

}

// Withdraw — withdraw GPoints to pay a new order.
//
// POST /api/user/balance/withdraw
func (h Handlers) Withdraw(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "Withdraw").Logger()
	if !checkContentType(r, "application/json") {
		log.Error().Msgf("wrong content type: %s", r.Header.Get("Content-type"))
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}

	dec := json.NewDecoder(r.Body)
	defer r.Body.Close()
	withdrawal := model.Withdrawal{}
	if err := dec.Decode(&withdrawal); err != nil {
		log.Error().Err(err).Msg("unmarshalling request body")
		http.Error(w, "Bad request", http.StatusBadRequest)

		return
	}
	log = log.With().Str("orderID", withdrawal.OrderID.String()).Float32("sum", withdrawal.Sum).Logger()

	if !withdrawal.OrderID.Valid() {
		log.Error().Msg("Invalid order number")
		http.Error(w, "Incorrect order number format", http.StatusUnprocessableEntity)

		return
	}

	if err := h.svc.Withdraw(r.Context(), withdrawal.OrderID, withdrawal.Sum); err != nil {
		if errors.Is(err, storage.ErrInsufficientPoints) {
			log.Error().Err(err).Msg("withdrawing points")
			http.Error(w, "Insufficient points", http.StatusPaymentRequired)

			return
		}
		log.Error().Err(err).Msg("withdrawing points")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

	log.Info().Msg("successfully withdrawed")
	w.WriteHeader(http.StatusOK)
}

// GetWithdrawals  — get withdrawal information from a user's bonus account.
//
// GET /api/user/balance/withdrawals
func (h Handlers) GetWithdrawals(w http.ResponseWriter, r *http.Request) {
	log := appContext.Logger(r.Context()).With().Str("handler", "Withdraw").Logger()

	withdrawals, err := h.svc.GetWithdrawals(r.Context())
	if err != nil {
		log.Error().Err(err).Msg("fetching user's withdrawals")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	if err := enc.Encode(withdrawals); err != nil {
		log.Error().Err(err).Msg("fetching user's withdrawals")
		http.Error(w, "Internal server error", http.StatusInternalServerError)

		return
	}
}

// signIn creates a remember token and stores it in DB and in the user's cookie.
func (h Handlers) signIn(w http.ResponseWriter, r *http.Request, user model.User) error {
	remember, err := generateToken()
	if err != nil {
		return err
	}
	user.RememberToken = remember
	if err := h.db.UpdateUser(r.Context(), user); err != nil {
		return err
	}
	cookie := http.Cookie{
		Name:  cookieName,
		Value: remember,
	}
	http.SetCookie(w, &cookie)

	return nil
}

// generateToken creates a random token.
func generateToken() (string, error) {
	b := make([]byte, rememberTokenSize)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}

	return base64.URLEncoding.EncodeToString(b), nil
}

// checkContentType checks whether "Content-type" field in the request contains the string provided.
func checkContentType(r *http.Request, wantContentType string) bool {
	return strings.Contains(r.Header.Get("Content-Type"), wantContentType)
}
