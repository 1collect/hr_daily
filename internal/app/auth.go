package app

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type authContextKey struct{}

type sessionClaims struct {
	UserID     string `json:"uid"`
	EmployeeID string `json:"eid,omitempty"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	ExpiresAt  int64  `json:"exp"`
}

type currentUser struct {
	ID         string `json:"id"`
	EmployeeID string `json:"employeeId,omitempty"`
	Username   string `json:"username"`
	Role       string `json:"role"`
	FirstName  string `json:"firstName,omitempty"`
	LastName   string `json:"lastName,omitempty"`
	MiddleName string `json:"middleName,omitempty"`
}

func (a *App) ensureSuperadmin(ctx context.Context, username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = a.db.Exec(ctx, `INSERT INTO users(username,password_hash,role,active,system)
		VALUES($1,$2,'superadmin',true,true)
		ON CONFLICT (lower(username)) DO UPDATE SET role='superadmin',active=true,system=true`, strings.TrimSpace(username), string(hash))
	return err
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	var u currentUser
	var hash string
	err := a.db.QueryRow(r.Context(), `SELECT u.id,COALESCE(u.employee_id::text,''),u.username,u.role,u.password_hash,
		COALESCE(e.first_name,''),COALESCE(e.last_name,''),COALESCE(e.middle_name,'')
		FROM users u LEFT JOIN employees e ON e.id=u.employee_id
		WHERE lower(u.username)=lower($1) AND u.active`, strings.TrimSpace(in.Username)).
		Scan(&u.ID, &u.EmployeeID, &u.Username, &u.Role, &hash, &u.FirstName, &u.LastName, &u.MiddleName)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil {
		problem(w, http.StatusUnauthorized, "Неверный логин или пароль")
		return
	}
	claims := sessionClaims{UserID: u.ID, EmployeeID: u.EmployeeID, Username: u.Username, Role: u.Role, ExpiresAt: time.Now().Add(12 * time.Hour).Unix()}
	token, err := a.signSession(claims)
	if err != nil {
		serverError(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "hr_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: 12 * 60 * 60})
	jsonOut(w, 200, u)
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "hr_session", Value: "", Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil, MaxAge: -1})
	jsonOut(w, 200, map[string]bool{"ok": true})
}

func (a *App) me(w http.ResponseWriter, r *http.Request) {
	c := claimsFrom(r.Context())
	var u currentUser
	err := a.db.QueryRow(r.Context(), `SELECT u.id,COALESCE(u.employee_id::text,''),u.username,u.role,COALESCE(e.first_name,''),COALESCE(e.last_name,''),COALESCE(e.middle_name,'') FROM users u LEFT JOIN employees e ON e.id=u.employee_id WHERE u.id=$1 AND u.active`, c.UserID).Scan(&u.ID, &u.EmployeeID, &u.Username, &u.Role, &u.FirstName, &u.LastName, &u.MiddleName)
	if err != nil {
		problem(w, 401, "Сессия недействительна")
		return
	}
	jsonOut(w, 200, u)
}

func (a *App) signSession(c sessionClaims) (string, error) {
	body, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(payload))
	signature := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return payload + "." + signature, nil
}
func (a *App) parseSession(token string) (sessionClaims, error) {
	var c sessionClaims
	parts := strings.Split(token, ".")
	if len(parts) != 2 {
		return c, errors.New("invalid token")
	}
	mac := hmac.New(sha256.New, a.secret)
	_, _ = mac.Write([]byte(parts[0]))
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !hmac.Equal(sig, mac.Sum(nil)) {
		return c, errors.New("invalid signature")
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return c, err
	}
	if err = json.Unmarshal(body, &c); err != nil {
		return c, err
	}
	if c.ExpiresAt < time.Now().Unix() {
		return c, errors.New("expired")
	}
	return c, nil
}

func (a *App) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" || r.URL.Path == "/api/auth/login" || (!strings.HasPrefix(r.URL.Path, "/api/") && !strings.HasPrefix(r.URL.Path, "/ws/")) {
			next.ServeHTTP(w, r)
			return
		}
		cookie, err := r.Cookie("hr_session")
		if err != nil {
			problem(w, 401, "Требуется авторизация")
			return
		}
		claims, err := a.parseSession(cookie.Value)
		if err != nil {
			problem(w, 401, "Требуется авторизация")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), authContextKey{}, claims)))
	})
}
func claimsFrom(ctx context.Context) sessionClaims {
	c, _ := ctx.Value(authContextKey{}).(sessionClaims)
	return c
}
func isManager(c sessionClaims) bool { return c.Role == "admin" || c.Role == "superadmin" }
func requireManager(w http.ResponseWriter, r *http.Request) (sessionClaims, bool) {
	c := claimsFrom(r.Context())
	if !isManager(c) {
		problem(w, 403, "Недостаточно прав")
		return c, false
	}
	return c, true
}
