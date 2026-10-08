package handlers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"
)

var ErrCorreoNoConfigurado = errors.New("El envío de correo todavía no está configurado.")

type RemitenteOferta interface {
	Enviar(context.Context, string, string, string) error
}

// SMTPOferta envía códigos y constancias sin incluir el código en logs o API.
type SMTPOferta struct {
	Host, Port, Usuario, Clave, Desde string
}

// Mailpit confirma la recepción SMTP, pero nunca entrega el mensaje
// a un buzón externo. Informarlo evita presentar una prueba local como
// si fuera un envío real al cliente.
func correoEnModoPrueba(remitente RemitenteOferta) bool {
	switch smtp := remitente.(type) {
	case SMTPOferta:
		return strings.EqualFold(smtp.Host, "mailpit")
	case *SMTPOferta:
		return smtp != nil && strings.EqualFold(smtp.Host, "mailpit")
	default:
		return false
	}
}

func (s SMTPOferta) Enviar(ctx context.Context, destino, asunto, cuerpo string) error {
	if s.Host == "" || s.Desde == "" {
		return ErrCorreoNoConfigurado
	}
	if !patronCorreoValido.MatchString(destino) || !patronCorreoValido.MatchString(s.Desde) || strings.ContainsAny(asunto, "\r\n") {
		return errors.New("Dirección o asunto de correo inválido.")
	}
	// Un servidor externo (Gmail incluido) no debe intentar enviar sin
	// autenticación: Mailpit sí permite pruebas locales sin credenciales.
	remoto := s.Host != "localhost" && s.Host != "127.0.0.1" && s.Host != "mailpit"
	if remoto && (s.Usuario == "" || s.Clave == "") {
		return ErrCorreoNoConfigurado
	}
	puerto := s.Port
	if puerto == "" {
		puerto = "587"
	}
	conexion, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(s.Host, puerto))
	if err != nil {
		return err
	}
	defer conexion.Close()
	cliente, err := smtp.NewClient(conexion, s.Host)
	if err != nil {
		return err
	}
	defer cliente.Close()
	if disponible, _ := cliente.Extension("STARTTLS"); disponible {
		if err := cliente.StartTLS(&tls.Config{ServerName: s.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return err
		}
	} else if remoto {
		return errors.New("El servidor SMTP remoto debe admitir STARTTLS.")
	}
	if s.Usuario != "" {
		if err := cliente.Auth(smtp.PlainAuth("", s.Usuario, s.Clave, s.Host)); err != nil {
			return err
		}
	}
	if err := cliente.Mail(s.Desde); err != nil {
		return err
	}
	if err := cliente.Rcpt(destino); err != nil {
		return err
	}
	escritor, err := cliente.Data()
	if err != nil {
		return err
	}
	mensaje := fmt.Sprintf("From: %s\r\nTo: %s\r\nSubject: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n%s\r\n", s.Desde, destino, mime.QEncoding.Encode("utf-8", asunto), strings.ReplaceAll(cuerpo, "\n", "\r\n"))
	if _, err := escritor.Write([]byte(mensaje)); err != nil {
		escritor.Close()
		return err
	}
	if err := escritor.Close(); err != nil {
		return err
	}
	return cliente.Quit()
}
