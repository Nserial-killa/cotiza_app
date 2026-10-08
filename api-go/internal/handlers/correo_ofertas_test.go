package handlers

import (
	"context"
	"os"
	"testing"
	"time"
)

// Prueba opcional contra Mailpit: no envía correo fuera de la máquina.
func TestSMTPOferta_MailpitLocal(t *testing.T) {
	if os.Getenv("TEST_MAILPIT") != "1" {
		t.Skip("Defina TEST_MAILPIT=1 para probar el SMTP local.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	remitente := SMTPOferta{Host: "127.0.0.1", Port: "1025", Desde: "cotiza@localhost.test"}
	if err := remitente.Enviar(ctx, "prueba@localhost.test", "Prueba de código de aceptación", "Correo de prueba local.\n"); err != nil {
		t.Fatal(err)
	}
}

func TestCorreoEnModoPrueba_DistingueMailpitDeSMTPReal(t *testing.T) {
	if !correoEnModoPrueba(SMTPOferta{Host: "mailpit"}) {
		t.Fatal("Mailpit debe identificarse como buzón de pruebas")
	}
	if correoEnModoPrueba(SMTPOferta{Host: "smtp.empresa.example"}) {
		t.Fatal("un servidor SMTP externo no debe identificarse como Mailpit")
	}
	if correoEnModoPrueba(nil) {
		t.Fatal("sin remitente no existe entrega de prueba")
	}
}

func TestSMTPOferta_GmailRequiereCredencial(t *testing.T) {
	remitente := SMTPOferta{
		Host: "smtp.gmail.com", Port: "587", Usuario: "remitente@gmail.com", Desde: "remitente@gmail.com",
	}
	err := remitente.Enviar(context.Background(), "destino@gmail.com", "Prueba", "Código de prueba")
	if err != ErrCorreoNoConfigurado {
		t.Fatalf("se esperaba configuración pendiente sin contraseña de aplicación, se obtuvo %v", err)
	}
}
