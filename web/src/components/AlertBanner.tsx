interface AlertBannerProps {
  message?: string;
}

function AlertBanner({ message }: AlertBannerProps) {
  if (!message) {
    return null;
  }
  return (
    <div role="alert" className="alert-banner">
      {message}
    </div>
  );
}

export default AlertBanner;
