variable "compartment_id" { type = string }
variable "availability_domain" { type = string }
variable "arm_image_id" { type = string } # Oracle Linux 10 aarch64
variable "os_namespace" { type = string }
variable "ssh_public_key" { type = string }

variable "public_tcp_ports" {
  type        = list(number)
  default     = [25565, 443, 80]
  description = "Ports TCP publics. 22 est volontairement absent : accès par Bastion."
}

variable "tenancy_ocid" { type = string }
variable "alert_email" {
  type        = string
  description = "Destinataire des alertes de budget. Le seul vrai garde-fou en Pay As You Go."
}

variable "profil_oci" {
  type    = string
  default = "CaffeLatte"
  # Profil de ~/.oci/config. Par defaut le profil interactif, a jeton de session.
  # La boucle d'attente de capacite passe un profil a cle d'API, qui lui ne
  # meurt pas au bout d'une heure.
}

variable "auth_oci" {
  type    = string
  default = "SecurityToken"
  # Doit suivre profil_oci : "SecurityToken" pour un profil a jeton de session,
  # "ApiKey" pour un profil a cle d'API. Un mode applique au mauvais type de
  # profil echoue en 401, la meme panne qui a coute la session du 2026-09-19.
  validation {
    condition     = contains(["SecurityToken", "ApiKey"], var.auth_oci)
    error_message = "auth_oci doit valoir SecurityToken ou ApiKey."
  }
}
