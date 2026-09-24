package app

import (
	"net/http"
	"strings"
)

type whatsappWABAConfig struct {
	ID            string `json:"id"`
	EmployeeID    string `json:"employeeId"`
	EmployeeName  string `json:"employeeName"`
	PhoneNumber   string `json:"phoneNumber"`
	DisplayName   string `json:"displayName"`
	WABAID        string `json:"wabaId"`
	PhoneNumberID string `json:"phoneNumberId"`
	AccessToken   string `json:"-"`
	VerifyToken   string `json:"-"`
	AppSecret     string `json:"-"`
	Active        bool   `json:"active"`
	TransportMode string `json:"transportMode"`
}

type whatsappWABAConfigInput struct {
	EmployeeID    string `json:"employeeId"`
	PhoneNumber   string `json:"phoneNumber"`
	DisplayName   string `json:"displayName"`
	WABAID        string `json:"wabaId"`
	PhoneNumberID string `json:"phoneNumberId"`
	AccessToken   string `json:"accessToken"`
	VerifyToken   string `json:"verifyToken"`
	AppSecret     string `json:"appSecret"`
	Active        bool   `json:"active"`
	TransportMode string `json:"transportMode"`
}

func (a *App) whatsappWABAConfigs(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	rows, err := a.db.Query(r.Context(), `SELECT c.id,c.employee_id,
		trim(concat_ws(' ',e.last_name,e.first_name,e.middle_name)),c.phone_number,c.display_name,
		c.waba_id,c.phone_number_id,c.access_token,c.verify_token,c.app_secret,c.active,c.transport_mode
		FROM whatsapp_waba_configs c JOIN employees e ON e.id=c.employee_id
		ORDER BY e.last_name,e.first_name,e.middle_name,c.phone_number`)
	if err != nil {
		serverError(w, err)
		return
	}
	defer rows.Close()
	out := []whatsappWABAConfig{}
	for rows.Next() {
		var item whatsappWABAConfig
		if err = rows.Scan(&item.ID, &item.EmployeeID, &item.EmployeeName, &item.PhoneNumber, &item.DisplayName,
			&item.WABAID, &item.PhoneNumberID, &item.AccessToken, &item.VerifyToken, &item.AppSecret, &item.Active, &item.TransportMode); err != nil {
			serverError(w, err)
			return
		}
		out = append(out, item)
	}
	if err = rows.Err(); err != nil {
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusOK, out)
}

func validateWhatsAppWABAConfig(in *whatsappWABAConfigInput) string {
	in.EmployeeID = strings.TrimSpace(in.EmployeeID)
	in.PhoneNumber = strings.TrimSpace(in.PhoneNumber)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.WABAID = strings.TrimSpace(in.WABAID)
	in.PhoneNumberID = strings.TrimSpace(in.PhoneNumberID)
	in.AccessToken = strings.TrimSpace(in.AccessToken)
	in.VerifyToken = strings.TrimSpace(in.VerifyToken)
	in.AppSecret = strings.TrimSpace(in.AppSecret)
	in.TransportMode = strings.TrimSpace(in.TransportMode)
	if in.TransportMode == "" {
		in.TransportMode = "whatsapp"
	}
	if in.EmployeeID == "" {
		return "Выберите сотрудника"
	}
	if in.PhoneNumber == "" {
		return "Укажите номер WhatsApp"
	}
	if in.WABAID == "" || in.PhoneNumberID == "" {
		return "Укажите WABA ID и Phone Number ID"
	}
	if in.TransportMode != "terminal" && in.TransportMode != "whatsapp" && in.TransportMode != "whatsapp_test" {
		return "Недопустимый режим WhatsApp"
	}
	return ""
}

func (a *App) createWhatsAppWABAConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var in whatsappWABAConfigInput
	if !decode(w, r, &in) {
		return
	}
	if msg := validateWhatsAppWABAConfig(&in); msg != "" {
		problem(w, http.StatusUnprocessableEntity, msg)
		return
	}
	var item whatsappWABAConfig
	err := a.db.QueryRow(r.Context(), `INSERT INTO whatsapp_waba_configs
		(employee_id,phone_number,display_name,waba_id,phone_number_id,access_token,verify_token,app_secret,active,transport_mode)
		SELECT e.id,$2,$3,$4,$5,$6,$7,$8,$9,$10 FROM employees e WHERE e.id=$1 AND e.active
		RETURNING id,employee_id,phone_number,display_name,waba_id,phone_number_id,access_token,verify_token,app_secret,active,transport_mode`,
		in.EmployeeID, in.PhoneNumber, in.DisplayName, in.WABAID, in.PhoneNumberID, in.AccessToken, in.VerifyToken, in.AppSecret, in.Active, in.TransportMode).
		Scan(&item.ID, &item.EmployeeID, &item.PhoneNumber, &item.DisplayName, &item.WABAID, &item.PhoneNumberID, &item.AccessToken, &item.VerifyToken, &item.AppSecret, &item.Active, &item.TransportMode)
	if err != nil {
		if strings.Contains(err.Error(), "whatsapp_waba_configs_employee_id_key") {
			problem(w, http.StatusConflict, "У этого сотрудника уже настроен WhatsApp номер")
			return
		}
		if strings.Contains(err.Error(), "no rows") {
			problem(w, http.StatusUnprocessableEntity, "Сотрудник не найден или неактивен")
			return
		}
		serverError(w, err)
		return
	}
	jsonOut(w, http.StatusCreated, item)
}

func (a *App) updateWhatsAppWABAConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	var in whatsappWABAConfigInput
	if !decode(w, r, &in) {
		return
	}
	if msg := validateWhatsAppWABAConfig(&in); msg != "" {
		problem(w, http.StatusUnprocessableEntity, msg)
		return
	}
	var item whatsappWABAConfig
	err := a.db.QueryRow(r.Context(), `UPDATE whatsapp_waba_configs c SET employee_id=$2,phone_number=$3,display_name=$4,
		waba_id=$5,phone_number_id=$6,access_token=CASE WHEN $7<>'' THEN $7 ELSE c.access_token END,verify_token=CASE WHEN $8<>'' THEN $8 ELSE c.verify_token END,app_secret=CASE WHEN $9<>'' THEN $9 ELSE c.app_secret END,active=$10,transport_mode=$11,updated_at=now()
		FROM employees e WHERE c.id=$1 AND e.id=$2 AND e.active
		RETURNING c.id,c.employee_id,c.phone_number,c.display_name,c.waba_id,c.phone_number_id,c.access_token,c.verify_token,c.app_secret,c.active,c.transport_mode`,
		r.PathValue("id"), in.EmployeeID, in.PhoneNumber, in.DisplayName, in.WABAID, in.PhoneNumberID, in.AccessToken, in.VerifyToken, in.AppSecret, in.Active, in.TransportMode).
		Scan(&item.ID, &item.EmployeeID, &item.PhoneNumber, &item.DisplayName, &item.WABAID, &item.PhoneNumberID, &item.AccessToken, &item.VerifyToken, &item.AppSecret, &item.Active, &item.TransportMode)
	if err != nil {
		if strings.Contains(err.Error(), "whatsapp_waba_configs_employee_id_key") {
			problem(w, http.StatusConflict, "У этого сотрудника уже настроен WhatsApp номер")
			return
		}
		problem(w, http.StatusNotFound, "WhatsApp конфигурация не найдена или сотрудник неактивен")
		return
	}
	jsonOut(w, http.StatusOK, item)
}

func (a *App) deleteWhatsAppWABAConfig(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireManager(w, r); !ok {
		return
	}
	result, err := a.db.Exec(r.Context(), `DELETE FROM whatsapp_waba_configs WHERE id=$1`, r.PathValue("id"))
	if err != nil {
		serverError(w, err)
		return
	}
	if result.RowsAffected() == 0 {
		problem(w, http.StatusNotFound, "WhatsApp конфигурация не найдена")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
