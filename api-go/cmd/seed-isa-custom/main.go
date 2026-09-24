// Comando de integración: toda modificación de negocio pasa por el API público.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"cotiza/api/internal/isacustom"
)

func main() {
	api := flag.String("api", "http://localhost:8080", "URL base del API")
	correo := flag.String("correo", "demo.admin@exceltecgroup.com", "Administrador de demostración")
	prefijo := flag.String("prefijo", "ISA-CUSTOM", "Prefijo estable de entidades del seed")
	diagnostico := flag.Bool("diagnostico", false, "Publicar también piezas parciales para diagnosticar; el proceso sigue fallando si hay incidencias")
	salida := flag.String("informe", "", "Archivo JSON de evidencia (sin credenciales)")
	flag.Parse()
	// PIN por stdin, nunca por argumento, log o archivo de evidencia.
	var pin string
	if _, err := fmt.Fscanln(os.Stdin, &pin); err != nil {
		fmt.Fprintln(os.Stderr, "Debe proporcionar el PIN por entrada estándar.")
		os.Exit(1)
	}
	datos, err := isacustom.CargarDatos()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cliente := isacustom.NuevoCliente(*api, *prefijo)
	if err := cliente.Login(ctx, *correo, pin); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	informe := cliente.Sembrar(ctx, datos, *diagnostico)
	cliente.Logout(ctx)
	if *salida != "" {
		raw, err := json.MarshalIndent(informe, "", "  ")
		if err == nil {
			err = os.WriteFile(*salida, append(raw, '\n'), 0600)
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	fmt.Printf("ISA Custom: completo=%t; compilado=%t; diagnóstico=%t\n", informe.Completo, informe.Compilado, informe.Diagnostico)
	fmt.Printf("Cotizador: %s\n  %s/api/cotizador/tabs?calculadora_id=%s\n", informe.CalculadoraID, *api, informe.CalculadoraID)
	fmt.Printf("Cliente: %s\nCotización: %s\n  %s/api/cotizador/runtime/%s\nPlantilla: %s\n  %s/api/plantillas/%s\n", informe.ClienteID, informe.CotizacionID, *api, informe.CotizacionID, informe.PlantillaID, *api, informe.PlantillaID)
	fmt.Printf("Vista previa (requiere sesión): %s/api/cotizaciones/%s/vista-previa-oferta\n", *api, informe.CotizacionID)
	for _, i := range informe.Incidencias {
		fmt.Printf("FALLA [%s] HTTP %d: %s\n", i.Paso, i.EstadoHTTP, i.Detalle)
	}
	if !informe.Completo {
		fmt.Printf("NO APROBADO: %d incidencias. No usar el compilado parcial como oferta comercial.\n", len(informe.Incidencias))
		os.Exit(2)
	}
	fmt.Println("Caso completo configurado por el API; ejecutar CP-01 a CP-15 para validar su comportamiento.")
}
