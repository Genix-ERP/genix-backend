package email

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"html/template"
	"net/smtp"
	"strings"

	"github.com/genixerp/genix-backend/internal/config"
)

// Service handles email sending
type Service struct {
	config *config.EmailConfig
}

// NewService creates a new email service
func NewService(cfg *config.EmailConfig) *Service {
	return &Service{config: cfg}
}

// Email represents an email message
type Email struct {
	To      []string
	Subject string
	Body    string
	IsHTML  bool
}

// Send sends an email
func (s *Service) Send(email *Email) error {
	fmt.Printf("[EMAIL] Attempting to send email - Host: %s, Port: %d, To: %s\n",
		s.config.SMTPHost, s.config.SMTPPort, strings.Join(email.To, ", "))

	if s.config.Provider == "console" || s.config.SMTPHost == "" || s.config.SMTPHost == "localhost" {
		// In development, just log the email
		fmt.Printf("[EMAIL] Development mode - logging only\n")
		fmt.Printf("[EMAIL] To: %s, Subject: %s\n", strings.Join(email.To, ", "), email.Subject)
		fmt.Printf("[EMAIL] Body: %s\n", email.Body)
		return nil
	}

	fmt.Printf("[EMAIL] Production mode - sending via SMTP\n")

	from := s.config.FromEmail
	if s.config.FromName != "" {
		from = fmt.Sprintf("%s <%s>", s.config.FromName, s.config.FromEmail)
	}

	// Build message
	var msg bytes.Buffer
	msg.WriteString(fmt.Sprintf("From: %s\r\n", from))
	msg.WriteString(fmt.Sprintf("To: %s\r\n", strings.Join(email.To, ", ")))
	msg.WriteString(fmt.Sprintf("Subject: %s\r\n", email.Subject))

	if email.IsHTML {
		msg.WriteString("MIME-Version: 1.0\r\n")
		msg.WriteString("Content-Type: text/html; charset=\"UTF-8\"\r\n")
	} else {
		msg.WriteString("Content-Type: text/plain; charset=\"UTF-8\"\r\n")
	}

	msg.WriteString("\r\n")
	msg.WriteString(email.Body)

	// Connect to SMTP server
	addr := fmt.Sprintf("%s:%d", s.config.SMTPHost, s.config.SMTPPort)

	var auth smtp.Auth
	if s.config.Username != "" {
		auth = smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.SMTPHost)
	}

	// Use TLS for port 465, STARTTLS for 587
	if s.config.SMTPPort == 465 {
		fmt.Printf("[EMAIL] Using TLS (port 465)\n")
		err := s.sendWithTLS(addr, auth, s.config.FromEmail, email.To, msg.Bytes())
		if err != nil {
			fmt.Printf("[EMAIL] Failed to send via TLS: %v\n", err)
		} else {
			fmt.Printf("[EMAIL] Successfully sent via TLS\n")
		}
		return err
	}

	fmt.Printf("[EMAIL] Using STARTTLS (port 587)\n")
	err := smtp.SendMail(addr, auth, s.config.FromEmail, email.To, msg.Bytes())
	if err != nil {
		fmt.Printf("[EMAIL] Failed to send via STARTTLS: %v\n", err)
	} else {
		fmt.Printf("[EMAIL] Successfully sent via STARTTLS\n")
	}
	return err
}

// sendWithTLS sends email using implicit TLS (port 465)
func (s *Service) sendWithTLS(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	tlsConfig := &tls.Config{
		ServerName: s.config.SMTPHost,
	}

	conn, err := tls.Dial("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, s.config.SMTPHost)
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer client.Close()

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("failed to authenticate: %w", err)
		}
	}

	if err := client.Mail(from); err != nil {
		return fmt.Errorf("failed to set sender: %w", err)
	}

	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("failed to set recipient: %w", err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("failed to get data writer: %w", err)
	}

	_, err = w.Write(msg)
	if err != nil {
		return fmt.Errorf("failed to write message: %w", err)
	}

	err = w.Close()
	if err != nil {
		return fmt.Errorf("failed to close writer: %w", err)
	}

	return client.Quit()
}

// SendInvite sends an invitation email
func (s *Service) SendInvite(toEmail, firstName, lastName, tenantName, inviteLink string) error {
	subject := fmt.Sprintf("Sizni %s kompaniyasiga taklif qilishdi", tenantName)

	body, err := s.renderTemplate(inviteTemplate, map[string]string{
		"FirstName":  firstName,
		"LastName":   lastName,
		"TenantName": tenantName,
		"InviteLink": inviteLink,
	})
	if err != nil {
		return err
	}

	return s.Send(&Email{
		To:      []string{toEmail},
		Subject: subject,
		Body:    body,
		IsHTML:  true,
	})
}

// SendPasswordReset sends a password reset email
func (s *Service) SendPasswordReset(toEmail, firstName, resetLink string) error {
	subject := "Parolni tiklash"

	body, err := s.renderTemplate(passwordResetTemplate, map[string]string{
		"FirstName": firstName,
		"ResetLink": resetLink,
	})
	if err != nil {
		return err
	}

	return s.Send(&Email{
		To:      []string{toEmail},
		Subject: subject,
		Body:    body,
		IsHTML:  true,
	})
}

// OTP email translations
type otpTranslation struct {
	SubjectRegistration  string
	SubjectPasswordReset string
	SubjectDefault       string
	Title                string
	Message              string
	CodeValidFor         string
	IgnoreMessage        string
	Footer               string
}

var otpTranslations = map[string]otpTranslation{
	"en": {
		SubjectRegistration:  "GenixERP - Email verification code",
		SubjectPasswordReset: "GenixERP - Password reset code",
		SubjectDefault:       "GenixERP - Verification code",
		Title:                "Verification Code",
		Message:              "Your verification code is:",
		CodeValidFor:         "This code is valid for 10 minutes.",
		IgnoreMessage:        "If you did not request this code, please ignore this message.",
		Footer:               "GenixERP - Modern ERP System",
	},
	"uz": {
		SubjectRegistration:  "GenixERP - Email tasdiqlash kodi",
		SubjectPasswordReset: "GenixERP - Parolni tiklash kodi",
		SubjectDefault:       "GenixERP - Tasdiqlash kodi",
		Title:                "Tasdiqlash kodi",
		Message:              "Sizning tasdiqlash kodingiz:",
		CodeValidFor:         "Bu kod 10 daqiqa davomida amal qiladi.",
		IgnoreMessage:        "Agar siz bu so'rovni yubormagan bo'lsangiz, bu xabarni e'tiborsiz qoldiring.",
		Footer:               "GenixERP - Zamonaviy ERP tizimi",
	},
	"ru": {
		SubjectRegistration:  "GenixERP - Код подтверждения email",
		SubjectPasswordReset: "GenixERP - Код сброса пароля",
		SubjectDefault:       "GenixERP - Код подтверждения",
		Title:                "Код подтверждения",
		Message:              "Ваш код подтверждения:",
		CodeValidFor:         "Этот код действителен в течение 10 минут.",
		IgnoreMessage:        "Если вы не запрашивали этот код, проигнорируйте это сообщение.",
		Footer:               "GenixERP - Современная ERP система",
	},
}

// SendOTP sends an OTP verification email with language support
func (s *Service) SendOTP(toEmail, otpCode, purpose, language string) error {
	// Default to Uzbek if language not specified
	if language == "" {
		language = "uz"
	}

	// Get translations, fallback to English if language not found
	trans, ok := otpTranslations[language]
	if !ok {
		trans = otpTranslations["en"]
	}

	var subject string
	switch purpose {
	case "registration":
		subject = trans.SubjectRegistration
	case "password_reset":
		subject = trans.SubjectPasswordReset
	default:
		subject = trans.SubjectDefault
	}

	body, err := s.renderTemplate(otpTemplateMultilang, map[string]string{
		"OTPCode":       otpCode,
		"Title":         trans.Title,
		"Message":       trans.Message,
		"CodeValidFor":  trans.CodeValidFor,
		"IgnoreMessage": trans.IgnoreMessage,
		"Footer":        trans.Footer,
	})
	if err != nil {
		return err
	}

	return s.Send(&Email{
		To:      []string{toEmail},
		Subject: subject,
		Body:    body,
		IsHTML:  true,
	})
}

// SendWelcome sends a welcome email
func (s *Service) SendWelcome(toEmail, firstName, tenantName string) error {
	subject := fmt.Sprintf("%s ga xush kelibsiz!", tenantName)

	body, err := s.renderTemplate(welcomeTemplate, map[string]string{
		"FirstName":  firstName,
		"TenantName": tenantName,
	})
	if err != nil {
		return err
	}

	return s.Send(&Email{
		To:      []string{toEmail},
		Subject: subject,
		Body:    body,
		IsHTML:  true,
	})
}

func (s *Service) renderTemplate(tmpl string, data map[string]string) (string, error) {
	t, err := template.New("email").Parse(tmpl)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", err
	}

	return buf.String(), nil
}

// Email templates
const inviteTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
        <h1 style="color: white; margin: 0; font-size: 24px;">GenixERP</h1>
    </div>
    <div style="background: #ffffff; padding: 30px; border: 1px solid #e0e0e0; border-top: none; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333; margin-top: 0;">Assalomu alaykum, {{.FirstName}}!</h2>
        <p>Sizni <strong>{{.TenantName}}</strong> kompaniyasiga taklif qilishdi.</p>
        <p>Hisobingizni faollashtirish va parol o'rnatish uchun quyidagi tugmani bosing:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="{{.InviteLink}}" style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 14px 30px; text-decoration: none; border-radius: 8px; font-weight: 600; display: inline-block;">Taklifni qabul qilish</a>
        </div>
        <p style="color: #666; font-size: 14px;">Agar tugma ishlamasa, quyidagi havolani brauzeringizga nusxalang:</p>
        <p style="color: #667eea; font-size: 14px; word-break: break-all;">{{.InviteLink}}</p>
        <hr style="border: none; border-top: 1px solid #e0e0e0; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; margin-bottom: 0;">Bu havola 48 soat davomida amal qiladi.</p>
    </div>
</body>
</html>`

const passwordResetTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
        <h1 style="color: white; margin: 0; font-size: 24px;">GenixERP</h1>
    </div>
    <div style="background: #ffffff; padding: 30px; border: 1px solid #e0e0e0; border-top: none; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333; margin-top: 0;">Parolni tiklash</h2>
        <p>Assalomu alaykum, {{.FirstName}}!</p>
        <p>Parolni tiklash so'rovi qabul qilindi. Yangi parol o'rnatish uchun quyidagi tugmani bosing:</p>
        <div style="text-align: center; margin: 30px 0;">
            <a href="{{.ResetLink}}" style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); color: white; padding: 14px 30px; text-decoration: none; border-radius: 8px; font-weight: 600; display: inline-block;">Parolni tiklash</a>
        </div>
        <p style="color: #666; font-size: 14px;">Agar siz bu so'rovni yubormagan bo'lsangiz, bu xabarni e'tiborsiz qoldiring.</p>
        <hr style="border: none; border-top: 1px solid #e0e0e0; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; margin-bottom: 0;">Bu havola 1 soat davomida amal qiladi.</p>
    </div>
</body>
</html>`

// Multi-language OTP template
const otpTemplateMultilang = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
        <h1 style="color: white; margin: 0; font-size: 24px;">GenixERP</h1>
    </div>
    <div style="background: #ffffff; padding: 30px; border: 1px solid #e0e0e0; border-top: none; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333; margin-top: 0;">{{.Title}}</h2>
        <p>{{.Message}}</p>
        <div style="text-align: center; margin: 30px 0;">
            <div style="background: #f5f5f5; padding: 20px 40px; border-radius: 10px; display: inline-block;">
                <span style="font-size: 32px; font-weight: bold; letter-spacing: 8px; color: #667eea;">{{.OTPCode}}</span>
            </div>
        </div>
        <p style="color: #666; font-size: 14px;">{{.CodeValidFor}}</p>
        <p style="color: #666; font-size: 14px;">{{.IgnoreMessage}}</p>
        <hr style="border: none; border-top: 1px solid #e0e0e0; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; margin-bottom: 0;">{{.Footer}}</p>
    </div>
</body>
</html>`

const welcomeTemplate = `<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
</head>
<body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif; line-height: 1.6; color: #333; max-width: 600px; margin: 0 auto; padding: 20px;">
    <div style="background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); padding: 30px; border-radius: 10px 10px 0 0; text-align: center;">
        <h1 style="color: white; margin: 0; font-size: 24px;">GenixERP</h1>
    </div>
    <div style="background: #ffffff; padding: 30px; border: 1px solid #e0e0e0; border-top: none; border-radius: 0 0 10px 10px;">
        <h2 style="color: #333; margin-top: 0;">Xush kelibsiz, {{.FirstName}}!</h2>
        <p>Siz <strong>{{.TenantName}}</strong> kompaniyasiga muvaffaqiyatli qo'shildingiz.</p>
        <p>Endi GenixERP tizimidan foydalanishingiz mumkin.</p>
        <hr style="border: none; border-top: 1px solid #e0e0e0; margin: 20px 0;">
        <p style="color: #999; font-size: 12px; margin-bottom: 0;">GenixERP - Zamonaviy ERP tizimi</p>
    </div>
</body>
</html>`
