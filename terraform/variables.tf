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
