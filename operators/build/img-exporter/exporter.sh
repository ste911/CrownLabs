#!/bin/sh
IMG_DIR=/data
OUT_DIR=/img-tmp
IMG_NAME=disk.img
OUT_IMAGE=vm-snapshot.qcow2
PROG_NAME=$0

usage(){
  echo "Usage: $PROG_NAME [-options]"
  echo "  -d, --img-dir        Specify the working directory [DEFAULT=$IMG_DIR]"
  echo "  -o, --out-dir        Specify the output directory  [DEFAULT=$OUT_DIR]"
  echo "  -n, --img-name       Specify the name of the image [DEFAULT=$OUT_IMAGE]"
  exit 1
}

error_handler(){
  # Skip if the exit status is 0
  [ $? -eq 0 ] && exit
  # Otherwise show the error message
  echo "Conversion unsuccessfully completed"
  exit 1
}

parse_args(){
  while [ "${1:-}" != "" ]; do
    case "$1" in
      "-d" | "--img-dir")
        shift
        IMG_DIR=$1
        ;;
      "-o" | "--out-dir")
	shift
        OUT_DIR=$1
        ;;
      "-n" | "--img-name")
        shift
	IMG_NAME=$1
        ;;
      *)
        usage
	;;
    esac
    shift
  done
}

trap error_handler EXIT

# Exit immediately if a command exits with a non-zero status
set -e

parse_args $*

echo "Converting the image..."
# Try the conversion of the image
qemu-img convert -c -f raw -O qcow2  "${IMG_DIR}/${IMG_NAME}" "${OUT_DIR}/${OUT_IMAGE}"

echo "Creating Dockerfile..."
# Create the Dockerfile
cat <<EOF > "${OUT_DIR}/Dockerfile"
FROM scratch
ADD ${OUT_IMAGE} /disk/
EOF


echo "${IMG_DIR}/${IMG_NAME} successully converted"
